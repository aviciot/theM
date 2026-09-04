package admin_test

// AL-05: MCP server audit write path tests.
//   AL-05a: NewMCPServersHandler accepts nil AuditWriter — no panic on construction.
//   AL-05b: AuditWriter.Write with nil receiver does not panic (MCP path coverage).

import (
	"testing"

	"github.com/aviciot/them/internal/admin"
	"github.com/aviciot/them/internal/admin/dal"
)

// AL-05a: Handler construction with nil audit writer must not panic.
func TestMCPServersHandler_NilAudit_NoPanic(t *testing.T) {
	db := &fakeDB{}
	// pools=nil forces the legacy path; nil audit exercises the nil-guard path.
	_ = admin.NewMCPServersHandler(db, nil, "test-secret", "", nil)
}

// AL-05b: AuditWriter.Write with a nil receiver must not panic.
// This covers the guard that all MCP write handlers rely on when audit is nil.
func TestMCPServersAuditWriter_NilReceiver_NoPanic(t *testing.T) {
	var aw *admin.AuditWriter
	aw.Write(nil, dal.AuditEntry{Action: "mcp_server.create", EntityType: "mcp_server"})
}
