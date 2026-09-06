package authserver

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

// UserMgmtHandlers exposes super_admin-only user management endpoints.
// Routes are mounted by RegisterUserMgmt; each handler extracts the caller's
// role from the Authorization header JWT before serving.
type UserMgmtHandlers struct {
	store  Store
	signer *tokenSigner
	log    *slog.Logger
}

// NewUserMgmtHandlers wires the handler set. cfg is used to build the token
// verifier; it must carry the same JWTSecret as the issuing auth service.
func NewUserMgmtHandlers(store Store, cfg *Config, log *slog.Logger) *UserMgmtHandlers {
	return &UserMgmtHandlers{
		store:  store,
		signer: NewTokenSigner(cfg),
		log:    log,
	}
}

// requireSuperAdmin extracts and verifies the Bearer token from the request,
// returning the verified claims or writing a 401/403 and returning nil.
func (h *UserMgmtHandlers) requireSuperAdmin(w http.ResponseWriter, r *http.Request) *verifiedClaims {
	token := bearerToken(r)
	if token == "" {
		writeErr(w, http.StatusUnauthorized, "Authorization header required")
		return nil
	}
	claims, err := h.signer.Verify(token)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "Invalid or expired token")
		return nil
	}
	if claims.Type != "access" {
		writeErr(w, http.StatusUnauthorized, "Access token required")
		return nil
	}
	if claims.Role != "super_admin" {
		writeErr(w, http.StatusForbidden, "super_admin role required")
		return nil
	}
	return claims
}

// ── request bodies ────────────────────────────────────────────────────────────

type createUserRequest struct {
	Username   string `json:"username"`
	Name       string `json:"name"`
	Email      string `json:"email"`
	Password   string `json:"password"`
	Role       string `json:"role"`
	TenantID   string `json:"tenant_id"`
	TenantRole string `json:"tenant_role"`
}

type updateUserRequest struct {
	Name   *string `json:"name"`
	Email  *string `json:"email"`
	Active *bool   `json:"active"`
}

type resetPasswordRequest struct {
	Password string `json:"password"`
}

// ── handlers ─────────────────────────────────────────────────────────────────

// ListUsers handles GET /api/v1/admin/users
func (h *UserMgmtHandlers) ListUsers(w http.ResponseWriter, r *http.Request) {
	if h.requireSuperAdmin(w, r) == nil {
		return
	}
	users, err := h.store.ListUsers(r.Context())
	if err != nil {
		h.log.Error("list users: db error", "error", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, users)
}

// GetUser handles GET /api/v1/admin/users/{id}
func (h *UserMgmtHandlers) GetUser(w http.ResponseWriter, r *http.Request) {
	if h.requireSuperAdmin(w, r) == nil {
		return
	}
	id, ok := parseUserID(w, r)
	if !ok {
		return
	}
	user, err := h.store.GetManagedUser(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			writeErr(w, http.StatusNotFound, "user not found")
			return
		}
		h.log.Error("get user: db error", "error", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, user)
}

// CreateUser handles POST /api/v1/admin/users
func (h *UserMgmtHandlers) CreateUser(w http.ResponseWriter, r *http.Request) {
	if h.requireSuperAdmin(w, r) == nil {
		return
	}
	var req createUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.Name = strings.TrimSpace(req.Name)
	if req.Username == "" || req.Name == "" || req.Password == "" {
		writeErr(w, http.StatusBadRequest, "username, name, and password are required")
		return
	}
	// req.Role is the tenant membership role (admin/member/viewer).
	// Global auth_service.roles always defaults to "viewer" for tenant users.
	if req.Role == "" {
		req.Role = "member"
	}

	hash, err := hashPassword(req.Password)
	if err != nil {
		h.log.Error("create user: hash password", "error", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}

	user, err := h.store.CreateUser(r.Context(), UserCreateInput{
		Username:     req.Username,
		Name:         req.Name,
		Email:        req.Email,
		PasswordHash: hash,
		RoleName:     "viewer", // global role — always viewer for tenant users
		TenantID:     req.TenantID,
		TenantRole:   req.Role, // tenant membership role from request
	})
	if err != nil {
		if errors.Is(err, ErrUserConflict) {
			writeErr(w, http.StatusConflict, "username or email already exists")
			return
		}
		h.log.Error("create user: db error", "error", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusCreated, user)
}

// UpdateUser handles PATCH /api/v1/admin/users/{id}
func (h *UserMgmtHandlers) UpdateUser(w http.ResponseWriter, r *http.Request) {
	if h.requireSuperAdmin(w, r) == nil {
		return
	}
	id, ok := parseUserID(w, r)
	if !ok {
		return
	}
	var req updateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	user, err := h.store.UpdateUser(r.Context(), id, UserUpdateInput{
		Name:   req.Name,
		Email:  req.Email,
		Active: req.Active,
	})
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			writeErr(w, http.StatusNotFound, "user not found")
			return
		}
		if errors.Is(err, ErrUserConflict) {
			writeErr(w, http.StatusConflict, "email already in use")
			return
		}
		h.log.Error("update user: db error", "error", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, user)
}

// DeleteUser handles DELETE /api/v1/admin/users/{id}
func (h *UserMgmtHandlers) DeleteUser(w http.ResponseWriter, r *http.Request) {
	if h.requireSuperAdmin(w, r) == nil {
		return
	}
	id, ok := parseUserID(w, r)
	if !ok {
		return
	}
	if err := h.store.DeleteUser(r.Context(), id); err != nil {
		if errors.Is(err, ErrUserNotFound) {
			writeErr(w, http.StatusNotFound, "user not found")
			return
		}
		h.log.Error("delete user: db error", "error", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ResetPassword handles POST /api/v1/admin/users/{id}/reset-password
func (h *UserMgmtHandlers) ResetPassword(w http.ResponseWriter, r *http.Request) {
	if h.requireSuperAdmin(w, r) == nil {
		return
	}
	id, ok := parseUserID(w, r)
	if !ok {
		return
	}
	var req resetPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Password == "" {
		writeErr(w, http.StatusBadRequest, "password is required")
		return
	}
	hash, err := hashPassword(req.Password)
	if err != nil {
		h.log.Error("reset password: hash error", "error", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := h.store.ResetPassword(r.Context(), id, hash); err != nil {
		if errors.Is(err, ErrUserNotFound) {
			writeErr(w, http.StatusNotFound, "user not found")
			return
		}
		h.log.Error("reset password: db error", "error", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "password updated"})
}

// ListTenants handles GET /api/v1/admin/tenants — used by the create-user form.
func (h *UserMgmtHandlers) ListTenants(w http.ResponseWriter, r *http.Request) {
	if h.requireSuperAdmin(w, r) == nil {
		return
	}
	tenants, err := h.store.ListTenants(r.Context())
	if err != nil {
		h.log.Error("list tenants: db error", "error", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, tenants)
}

// ── router registration ───────────────────────────────────────────────────────

// RegisterUserMgmt mounts the user management routes under base (e.g. /api/v1/admin).
func RegisterUserMgmt(r chi.Router, h *UserMgmtHandlers, base string) {
	r.Route(base, func(a chi.Router) {
		a.Get("/users", h.ListUsers)
		a.Post("/users", h.CreateUser)
		a.Get("/users/{id}", h.GetUser)
		a.Patch("/users/{id}", h.UpdateUser)
		a.Delete("/users/{id}", h.DeleteUser)
		a.Post("/users/{id}/reset-password", h.ResetPassword)
		a.Get("/tenants", h.ListTenants)
	})
}

// ── helpers ───────────────────────────────────────────────────────────────────

func parseUserID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid user id")
		return 0, false
	}
	return id, true
}
