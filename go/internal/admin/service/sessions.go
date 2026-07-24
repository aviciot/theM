package service

import (
	"context"

	"github.com/aviciot/them/internal/session"
)

// SessionReader is the admin service's view of the session store.
// *session.Store satisfies this interface structurally after Wave 5
// commit 1 added the two List methods and updated SignalDisconnect.
type SessionReader interface {
	ListEPSessions(ctx context.Context, epSlug string) ([]string, error)
	ListAppSessions(ctx context.Context, appID string) ([]string, error)
	Get(ctx context.Context, sessionID string) (*session.SessionInfo, error)
	SignalDisconnect(ctx context.Context, sessionID string) (bool, error)
}

// SessionListResult mirrors Python's {"sessions":[...], "count":N} response body.
type SessionListResult struct {
	Sessions []*session.SessionInfo `json:"sessions"`
	Count    int                    `json:"count"`
}

// SessionAdminService owns the business logic for session admin operations.
type SessionAdminService struct {
	sessions SessionReader
}

// NewSessionAdminService creates a SessionAdminService.
func NewSessionAdminService(r SessionReader) *SessionAdminService {
	return &SessionAdminService{sessions: r}
}

// ListByApp returns all live sessions for the given application ID.
// Ghost session IDs (no session hash in Redis) are silently dropped.
// Always returns a non-nil Sessions slice.
func (s *SessionAdminService) ListByApp(ctx context.Context, appID string) (SessionListResult, error) {
	return s.listByIDs(ctx, func() ([]string, error) {
		return s.sessions.ListAppSessions(ctx, appID)
	})
}

// ListByEP returns all live sessions for the given entry point slug.
// Ghost session IDs are silently dropped.
func (s *SessionAdminService) ListByEP(ctx context.Context, epSlug string) (SessionListResult, error) {
	return s.listByIDs(ctx, func() ([]string, error) {
		return s.sessions.ListEPSessions(ctx, epSlug)
	})
}

func (s *SessionAdminService) listByIDs(ctx context.Context, listFn func() ([]string, error)) (SessionListResult, error) {
	ids, err := listFn()
	if err != nil {
		return SessionListResult{Sessions: []*session.SessionInfo{}}, err
	}
	sessions := make([]*session.SessionInfo, 0, len(ids))
	for _, id := range ids {
		info, err := s.sessions.Get(ctx, id)
		if err != nil {
			// Session expired between list and get — silently drop (matches Python).
			continue
		}
		sessions = append(sessions, info)
	}
	return SessionListResult{Sessions: sessions, Count: len(sessions)}, nil
}

// Disconnect signals a session to close its WebSocket/SSE connection.
// Returns ErrNotFound if the session does not exist.
// Returns (true, nil) when the signal was published successfully.
func (s *SessionAdminService) Disconnect(ctx context.Context, sessionID string) (bool, error) {
	if _, err := s.sessions.Get(ctx, sessionID); err != nil {
		return false, ErrNotFound
	}
	return s.sessions.SignalDisconnect(ctx, sessionID)
}
