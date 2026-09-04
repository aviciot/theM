package admin_test

// AL-05: MCP server audit write path tests.
//   AL-05b: AuditWriter.Write with nil receiver does not panic (MCP path coverage).

import (
	"testing"

	"github.com/aviciot/them/internal/admin"
	"github.com/aviciot/them/internal/admin/dal"
)

// AL-05b: AuditWriter.Write with a nil receiver must not panic.
// This covers the guard that all MCP write handlers rely on when audit is nil.
func TestMCPServersAuditWriter_NilReceiver_NoPanic(t *testing.T) {
	var aw *admin.AuditWriter
	aw.Write(nil, dal.AuditEntry{Action: "mcp_server.create", EntityType: "mcp_server"})
}
