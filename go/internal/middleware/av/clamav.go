// Package av provides a ClamAV client that scans bytes via clamd.
// Supports both Unix socket ("unix:/path/to/clamd.sock" or a bare path) and
// TCP ("tcp:host:port" or "host:port") addresses.
// It implements middleware.Processor under the name "av_scan".
package av

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/aviciot/them/internal/middleware"
)

const Name = "av_scan"

// Scanner implements middleware.Processor using ClamAV clamd.
type Scanner struct {
	// addr is a dial address: "unix:/path", bare "/path", or "tcp:host:port" / "host:port".
	addr string
}

// New creates a Scanner for the given clamd address.
// addr may be:
//   - "/var/run/clamav/clamd.sock"   — bare Unix socket path
//   - "unix:/var/run/clamav/clamd.sock" — explicit Unix socket
//   - "them-clamd:3310"              — TCP host:port
//   - "tcp:them-clamd:3310"          — explicit TCP
func New(addr string) *Scanner {
	return &Scanner{addr: addr}
}

func (s *Scanner) Name() string { return Name }

// Process implements middleware.Processor.
// Only processes parts with Kind == "file"; skips all others.
func (s *Scanner) Process(ctx context.Context, part middleware.Part, cfgRaw json.RawMessage) (middleware.Result, error) {
	if part.Kind != "file" {
		return middleware.Result{Outcome: "skipped"}, nil
	}

	var cfg middleware.AVScanConfig
	if err := json.Unmarshal(cfgRaw, &cfg); err != nil {
		cfg = middleware.AVScanConfig{Enabled: true, MaxFileMB: 5, BlockOnInfected: true}
	}

	if !cfg.Enabled {
		return middleware.Result{Outcome: "skipped"}, nil
	}

	// Reject oversized files before scanning
	maxBytes := int64(cfg.MaxFileMB) * 1024 * 1024
	if maxBytes <= 0 {
		maxBytes = 5 * 1024 * 1024
	}
	if int64(len(part.Bytes)) > maxBytes {
		return middleware.Result{
			Outcome: "error",
			Block:   true,
			Detail:  map[string]any{"reason": fmt.Sprintf("file exceeds max_file_mb (%d)", cfg.MaxFileMB)},
		}, nil
	}

	if len(part.Bytes) == 0 {
		return middleware.Result{Outcome: "clean"}, nil
	}

	threat, err := s.scan(ctx, part.Bytes)
	if err != nil {
		// Scanner error — don't block (fail open), return error outcome
		return middleware.Result{
			Outcome: "error",
			Detail:  map[string]any{"reason": "scanner unavailable"},
		}, nil
	}

	if threat != "" {
		return middleware.Result{
			Outcome: "infected",
			Block:   cfg.BlockOnInfected,
			Detail:  map[string]any{"threat": threat},
		}, nil
	}

	return middleware.Result{Outcome: "clean"}, nil
}

// dialAddr returns (network, address) for net.DialTimeout from s.addr.
func (s *Scanner) dialAddr() (string, string) {
	switch {
	case strings.HasPrefix(s.addr, "unix:"):
		return "unix", strings.TrimPrefix(s.addr, "unix:")
	case strings.HasPrefix(s.addr, "tcp:"):
		return "tcp", strings.TrimPrefix(s.addr, "tcp:")
	case strings.HasPrefix(s.addr, "/"):
		// Bare Unix socket path
		return "unix", s.addr
	default:
		// host:port TCP
		return "tcp", s.addr
	}
}

// scan sends bytes to clamd via the INSTREAM command and returns the threat name
// (empty string = clean). Returns an error only on socket/protocol failure.
func (s *Scanner) scan(ctx context.Context, data []byte) (string, error) {
	deadline := 30 * time.Second
	if dl, ok := ctx.Deadline(); ok {
		if remaining := time.Until(dl) - 500*time.Millisecond; remaining > 0 && remaining < deadline {
			deadline = remaining
		}
	}

	network, address := s.dialAddr()
	conn, err := net.DialTimeout(network, address, 5*time.Second)
	if err != nil {
		return "", fmt.Errorf("clamd dial: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(deadline))

	// ClamAV INSTREAM protocol:
	//   zINSTREAM\0
	//   <4-byte big-endian chunk length><chunk bytes>
	//   <4-byte zero> (end of stream)
	//   Response: "stream: OK\n" or "stream: <ThreatName> FOUND\n"

	if _, err := conn.Write([]byte("zINSTREAM\x00")); err != nil {
		return "", fmt.Errorf("clamd write command: %w", err)
	}

	// Write data in chunks of 2048 bytes
	const chunkSize = 2048
	for off := 0; off < len(data); off += chunkSize {
		end := off + chunkSize
		if end > len(data) {
			end = len(data)
		}
		chunk := data[off:end]
		size := uint32(len(chunk))
		header := [4]byte{byte(size >> 24), byte(size >> 16), byte(size >> 8), byte(size)}
		if _, err := conn.Write(header[:]); err != nil {
			return "", fmt.Errorf("clamd write chunk header: %w", err)
		}
		if _, err := conn.Write(chunk); err != nil {
			return "", fmt.Errorf("clamd write chunk: %w", err)
		}
	}

	// Zero-length chunk = end of stream
	if _, err := conn.Write([]byte{0, 0, 0, 0}); err != nil {
		return "", fmt.Errorf("clamd write eos: %w", err)
	}

	// Read response
	resp, err := io.ReadAll(io.LimitReader(conn, 256))
	if err != nil {
		return "", fmt.Errorf("clamd read response: %w", err)
	}

	return parseClamdResponse(strings.Trim(string(resp), " \t\r\n\x00"))
}

// parseClamdResponse parses a clamd INSTREAM response.
// Returns ("", nil) for clean, (threat, nil) for infected, ("", err) for errors.
func parseClamdResponse(resp string) (string, error) {
	// Clean: "stream: OK"
	if strings.HasSuffix(resp, ": OK") {
		return "", nil
	}
	// Infected: "stream: Win.Trojan.Agent-1234 FOUND"
	if strings.HasSuffix(resp, " FOUND") {
		// Extract threat name: between ": " and " FOUND"
		after := strings.TrimPrefix(resp, "stream: ")
		threat := strings.TrimSuffix(after, " FOUND")
		if threat != "" {
			return threat, nil
		}
	}
	// Error response from clamd
	if strings.Contains(resp, "ERROR") || strings.Contains(resp, "error") {
		return "", fmt.Errorf("clamd error: %s", resp)
	}
	// Unexpected response — treat as scanner error (fail open)
	return "", fmt.Errorf("clamd unexpected response: %s", resp)
}
