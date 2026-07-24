package service

import (
	"context"
	"fmt"

	"github.com/aviciot/them/internal/admin/dal"
)

// epConfigChannel is the Redis pub/sub channel for cross-pod EP config cache invalidation.
// Must stay in sync with the Python platform's EP_CONFIG_CHANGED_CHANNEL constant.
const epConfigChannel = "them:ep:config:changed"

// validEPTypes is the canonical set of allowed entry_point_type values.
// Must stay in sync with the Python platform's _VALID_EP_TYPES list.
var validEPTypes = map[string]struct{}{
	"websocket": {},
	"sse":       {},
	"voice":     {},
	"webrtc":    {},
	"a2a":       {},
}

// IsValidEPType reports whether t is an allowed entry point type.
func IsValidEPType(t string) bool {
	_, ok := validEPTypes[t]
	return ok
}

// AppService owns the business logic for application and entry point CRUD.
type AppService struct {
	dal   Dal
	cache Cache
}

// NewAppService creates an AppService.
func NewAppService(d Dal, c Cache) *AppService {
	return &AppService{dal: d, cache: c}
}

// List returns all applications.
func (s *AppService) List(ctx context.Context) ([]dal.Application, error) {
	return s.dal.ListApplications(ctx)
}

// Get returns a single application with its entry points. Any DAL error maps
// to ErrNotFound to preserve the current API contract.
func (s *AppService) Get(ctx context.Context, id string) (dal.Application, error) {
	a, err := s.dal.GetApplication(ctx, id)
	if err != nil {
		return dal.Application{}, ErrNotFound
	}
	a.EntryPoints = s.dal.ListEntryPoints(ctx, id)
	return a, nil
}

// Create validates the input, persists, and returns the new ID.
func (s *AppService) Create(ctx context.Context, name string, enabled *bool) (string, error) {
	if name == "" {
		return "", validation("name is required")
	}
	return s.dal.CreateApplication(ctx, name, enabledOrDefault(enabled))
}

// Update persists changes and invalidates all EP slugs for the application.
func (s *AppService) Update(ctx context.Context, id, name string, enabled *bool) error {
	if err := s.dal.UpdateApplication(ctx, id, name, enabledOrDefault(enabled)); err != nil {
		return err
	}
	s.invalidateAppEPs(ctx, id)
	return nil
}

// Delete removes an application and invalidates all its EP slugs.
func (s *AppService) Delete(ctx context.Context, id string) error {
	s.invalidateAppEPs(ctx, id)
	if err := s.dal.DeleteApplication(ctx, id); err != nil {
		return err
	}
	return nil
}

// CreateEntryPoint validates the EP type, persists, and returns the new EP ID.
// No cache invalidation on create (nothing to evict for a new EP).
func (s *AppService) CreateEntryPoint(ctx context.Context, appID, slug, epType string, enabled *bool) (string, error) {
	if slug == "" || epType == "" {
		return "", validation("slug and entry_point_type are required")
	}
	if !IsValidEPType(epType) {
		return "", unprocessable("invalid entry_point_type: must be one of websocket, sse, voice, webrtc, a2a")
	}
	return s.dal.CreateEntryPoint(ctx, appID, slug, epType, enabledOrDefault(enabled))
}

// UpdateEntryPoint validates the EP type (if provided), persists, and publishes
// cache invalidation. On slug rename: old slug is published before new slug so
// that the old cache entry is evicted before the new one is written.
func (s *AppService) UpdateEntryPoint(ctx context.Context, epID, appID, slug, epType string, enabled *bool) error {
	if epType != "" && !IsValidEPType(epType) {
		return unprocessable("invalid entry_point_type: must be one of websocket, sse, voice, webrtc, a2a")
	}

	// Fetch old slug before the update for cache invalidation on rename.
	oldSlug, _ := s.dal.GetEntryPointSlug(ctx, epID, appID)

	if err := s.dal.UpdateEntryPoint(ctx, epID, appID, slug, epType, enabledOrDefault(enabled)); err != nil {
		return err
	}

	// Old slug must be published first so the stale cache entry is evicted before
	// the new slug is registered (critical ordering contract).
	s.publishEP(ctx, oldSlug)
	s.publishEP(ctx, slug)
	return nil
}

// DeleteEntryPoint fetches the slug, deletes the EP, and publishes invalidation.
func (s *AppService) DeleteEntryPoint(ctx context.Context, epID, appID string) error {
	epSlug, _ := s.dal.GetEntryPointSlug(ctx, epID, appID)
	if err := s.dal.DeleteEntryPoint(ctx, epID, appID); err != nil {
		return err
	}
	s.publishEP(ctx, epSlug)
	return nil
}

func (s *AppService) publishEP(ctx context.Context, slug string) {
	if s.cache == nil || slug == "" {
		return
	}
	_ = s.cache.Publish(ctx, epConfigChannel, slug)
}

func (s *AppService) invalidateAppEPs(ctx context.Context, appID string) {
	if s.cache == nil {
		return
	}
	for _, slug := range s.dal.ListEPSlugsForApp(ctx, appID) {
		_ = s.cache.Publish(ctx, fmt.Sprintf(epConfigChannel), slug)
	}
}
