package av_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/aviciot/them/internal/middleware"
	"github.com/aviciot/them/internal/middleware/av"
)

// startMockClamd starts a Unix socket server that mimics clamd responses.
// responses maps content fingerprints to response strings; "*" = default.
func startMockClamd(t *testing.T, response string) string {
	t.Helper()
	dir := t.TempDir()
	sock := filepath.Join(dir, "clamd.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("mock clamd listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				// Read the zINSTREAM command + all chunks until the zero-length terminator,
				// then write the response. The scanner does not close its write side before
				// reading — we must detect end-of-stream via the 4-byte zero chunk.
				buf := make([]byte, 65536)
				// Read command line (ends with \0)
				for i := 0; i < len(buf); i++ {
					n, err := c.Read(buf[i : i+1])
					if err != nil || n == 0 {
						return
					}
					if buf[i] == 0 {
						break
					}
				}
				// Read chunks until 4-byte zero terminator
				for {
					var hdr [4]byte
					if _, err := io.ReadFull(c, hdr[:]); err != nil {
						return
					}
					size := uint32(hdr[0])<<24 | uint32(hdr[1])<<16 | uint32(hdr[2])<<8 | uint32(hdr[3])
					if size == 0 {
						break // end of stream
					}
					remaining := int(size)
					for remaining > 0 {
						toRead := remaining
						if toRead > len(buf) {
							toRead = len(buf)
						}
						n, err := c.Read(buf[:toRead])
						if err != nil {
							return
						}
						remaining -= n
					}
				}
				c.Write([]byte(response + "\n"))
			}(conn)
		}
	}()

	return sock
}

func avCfg(enabled bool, maxMB int, block bool) json.RawMessage {
	b, _ := json.Marshal(middleware.AVScanConfig{
		Enabled: enabled, MaxFileMB: maxMB, BlockOnInfected: block,
	})
	return b
}

func TestAVScanner_CleanFile(t *testing.T) {
	sock := startMockClamd(t, "stream: OK")
	s := av.New(sock)

	part := middleware.Part{Kind: "file", Bytes: []byte("safe content"), FileName: "test.txt"}
	res, err := s.Process(context.Background(), part, avCfg(true, 5, true))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != "clean" {
		t.Errorf("expected clean, got %s", res.Outcome)
	}
	if res.Block {
		t.Error("clean file should not block")
	}
}

func TestAVScanner_InfectedFile_BlockEnabled(t *testing.T) {
	sock := startMockClamd(t, "stream: Win.Trojan.Agent-1234 FOUND")
	s := av.New(sock)

	part := middleware.Part{Kind: "file", Bytes: []byte("virus payload"), FileName: "evil.exe"}
	res, err := s.Process(context.Background(), part, avCfg(true, 5, true))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != "infected" {
		t.Errorf("expected infected, got %s", res.Outcome)
	}
	if !res.Block {
		t.Error("infected file with block_on_infected=true should block")
	}
	if res.Detail["threat"] != "Win.Trojan.Agent-1234" {
		t.Errorf("expected threat name, got %v", res.Detail["threat"])
	}
}

func TestAVScanner_InfectedFile_WarnOnly(t *testing.T) {
	sock := startMockClamd(t, "stream: Eicar-Test-Signature FOUND")
	s := av.New(sock)

	part := middleware.Part{Kind: "file", Bytes: []byte("eicar"), FileName: "eicar.txt"}
	res, err := s.Process(context.Background(), part, avCfg(true, 5, false))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != "infected" {
		t.Errorf("expected infected, got %s", res.Outcome)
	}
	if res.Block {
		t.Error("warn-only mode should not block")
	}
}

func TestAVScanner_OversizedFile_Blocks(t *testing.T) {
	sock := startMockClamd(t, "stream: OK")
	s := av.New(sock)

	big := make([]byte, 3*1024*1024) // 3MB > 2MB limit
	part := middleware.Part{Kind: "file", Bytes: big, FileName: "big.bin"}
	res, err := s.Process(context.Background(), part, avCfg(true, 2, true))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != "error" {
		t.Errorf("oversized file should return error outcome, got %s", res.Outcome)
	}
	if !res.Block {
		t.Error("oversized file should block")
	}
}

func TestAVScanner_NonFilePart_Skips(t *testing.T) {
	// No socket needed — should not be dialed for text parts
	s := av.New("/nonexistent/clamd.sock")
	part := middleware.Part{Kind: "text", Text: "hello world"}
	res, err := s.Process(context.Background(), part, avCfg(true, 5, true))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != "skipped" {
		t.Errorf("text part should be skipped, got %s", res.Outcome)
	}
}

func TestAVScanner_Disabled_Skips(t *testing.T) {
	s := av.New("/nonexistent/clamd.sock")
	part := middleware.Part{Kind: "file", Bytes: []byte("data")}
	res, err := s.Process(context.Background(), part, avCfg(false, 5, true))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != "skipped" {
		t.Errorf("disabled scanner should skip, got %s", res.Outcome)
	}
}

func TestAVScanner_SocketUnavailable_FailsOpen(t *testing.T) {
	s := av.New("/nonexistent/clamd.sock")
	part := middleware.Part{Kind: "file", Bytes: []byte("data"), FileName: "test.pdf"}
	res, err := s.Process(context.Background(), part, avCfg(true, 5, true))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Fail open: scanner unavailable = error outcome, not block
	if res.Outcome != "error" {
		t.Errorf("unavailable scanner should return error outcome, got %s", res.Outcome)
	}
	if res.Block {
		t.Error("scanner failure should fail open (no block)")
	}
}

func TestAVScanner_EmptyBytes_ReturnsClean(t *testing.T) {
	s := av.New("/nonexistent/clamd.sock") // not dialed for empty input
	part := middleware.Part{Kind: "file", Bytes: []byte{}}
	res, err := s.Process(context.Background(), part, avCfg(true, 5, true))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != "clean" {
		t.Errorf("empty bytes should be clean, got %s", res.Outcome)
	}
}

func TestAVScanner_Name(t *testing.T) {
	s := av.New("/tmp/clamd.sock")
	if s.Name() != "av_scan" {
		t.Errorf("expected av_scan, got %s", s.Name())
	}
}

// TestAVScanner_LiveClamd runs only when CLAMAV_SOCKET env var points at a live daemon.
func TestAVScanner_LiveClamd(t *testing.T) {
	sock := os.Getenv("CLAMAV_SOCKET")
	if sock == "" {
		t.Skip("CLAMAV_SOCKET not set — skipping live ClamAV test")
	}
	s := av.New(sock)

	// EICAR test string (safe — not a real virus, universally detected by AV)
	eicar := []byte(`X5O!P%@AP[4\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*`)
	part := middleware.Part{Kind: "file", Bytes: eicar, FileName: "eicar.txt"}
	res, err := s.Process(context.Background(), part, avCfg(true, 5, false))
	if err != nil {
		t.Fatalf("live scan error: %v", err)
	}
	if res.Outcome != "infected" {
		t.Errorf("EICAR should be detected as infected, got %s (threat: %v)", res.Outcome, res.Detail)
	}
}
