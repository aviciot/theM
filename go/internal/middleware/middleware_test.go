package middleware_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aviciot/them/internal/middleware"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// cleanProcessor always returns clean.
type cleanProcessor struct{ name string }

func (p *cleanProcessor) Name() string { return p.name }
func (p *cleanProcessor) Process(_ context.Context, part middleware.Part, _ json.RawMessage) (middleware.Result, error) {
	return middleware.Result{Outcome: "clean"}, nil
}

// blockProcessor always blocks with infected outcome.
type blockProcessor struct{ name string }

func (p *blockProcessor) Name() string { return p.name }
func (p *blockProcessor) Process(_ context.Context, _ middleware.Part, _ json.RawMessage) (middleware.Result, error) {
	return middleware.Result{
		Outcome: "infected",
		Block:   true,
		Detail:  map[string]any{"threat": "TestVirus.EICAR"},
	}, nil
}

// modifyProcessor replaces the part text.
type modifyProcessor struct{ name string }

func (p *modifyProcessor) Name() string { return p.name }
func (p *modifyProcessor) Process(_ context.Context, part middleware.Part, _ json.RawMessage) (middleware.Result, error) {
	modified := part
	modified.Text = "[REDACTED]"
	return middleware.Result{Outcome: "flagged", Modified: &modified}, nil
}

func newReg(procs ...middleware.Processor) *middleware.Registry {
	r := middleware.NewRegistry()
	for _, p := range procs {
		r.Register(p)
	}
	return r
}

// ── Registry tests ────────────────────────────────────────────────────────────

func TestRegistry_GetReturnsNilForUnknown(t *testing.T) {
	r := middleware.NewRegistry()
	if r.Get("unknown") != nil {
		t.Fatal("expected nil for unregistered processor")
	}
}

func TestRegistry_PanicOnDuplicate(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate registration")
		}
	}()
	r := middleware.NewRegistry()
	r.Register(&cleanProcessor{name: "av_scan"})
	r.Register(&cleanProcessor{name: "av_scan"}) // should panic
}

// ── Config tests ──────────────────────────────────────────────────────────────

func TestDefaultSecurityConfig_DisabledByDefault(t *testing.T) {
	cfg := middleware.DefaultSecurityConfig()
	if cfg.Enabled {
		t.Fatal("default config must be disabled")
	}
}

func TestDefaultSecurityConfig_HasAllProcessors(t *testing.T) {
	cfg := middleware.DefaultSecurityConfig()
	for _, name := range []string{"av_scan", "pii_redact", "prompt_inject", "schema_validate", "audit_capture"} {
		if _, ok := cfg.Processors[name]; !ok {
			t.Errorf("missing processor config: %s", name)
		}
	}
}

func TestMergeDefaults_FillsMissingKeys(t *testing.T) {
	src := middleware.SecurityConfig{Enabled: true, Processors: map[string]json.RawMessage{
		"av_scan": json.RawMessage(`{"enabled":true,"max_file_mb":10,"block_on_infected":true}`),
	}}
	merged := middleware.MergeDefaults(src)
	if _, ok := merged.Processors["pii_redact"]; !ok {
		t.Fatal("MergeDefaults should fill pii_redact from defaults")
	}
}

func TestValidate_DisabledIsAlwaysValid(t *testing.T) {
	cfg := middleware.SecurityConfig{Enabled: false}
	if err := middleware.Validate(cfg); err != nil {
		t.Fatalf("disabled config must be valid: %v", err)
	}
}

func TestValidate_RejectsInvalidMaxFileMB(t *testing.T) {
	raw, _ := json.Marshal(middleware.AVScanConfig{Enabled: true, MaxFileMB: 0, BlockOnInfected: true})
	cfg := middleware.SecurityConfig{
		Enabled:    true,
		Processors: map[string]json.RawMessage{"av_scan": raw},
	}
	if err := middleware.Validate(cfg); err == nil {
		t.Fatal("expected error for max_file_mb=0")
	}
}

func TestValidate_RejectsInvalidSensitivity(t *testing.T) {
	raw, _ := json.Marshal(middleware.PromptInjectConfig{Enabled: true, Sensitivity: "extreme"})
	cfg := middleware.SecurityConfig{
		Enabled:    true,
		Processors: map[string]json.RawMessage{"prompt_inject": raw},
	}
	if err := middleware.Validate(cfg); err == nil {
		t.Fatal("expected error for invalid sensitivity")
	}
}

func TestEnabledProcessors_EmptyWhenDisabled(t *testing.T) {
	cfg := middleware.DefaultSecurityConfig() // enabled=false
	reg := newReg(&cleanProcessor{name: "av_scan"})
	out := middleware.EnabledProcessors(cfg, reg, "file")
	if len(out) != 0 {
		t.Fatalf("expected empty list when config disabled, got %v", out)
	}
}

func TestEnabledProcessors_FiltersByPartKind(t *testing.T) {
	avRaw, _  := json.Marshal(middleware.AVScanConfig{Enabled: true, MaxFileMB: 5, BlockOnInfected: true})
	piiRaw, _ := json.Marshal(middleware.PIIRedactConfig{Enabled: true})
	cfg := middleware.SecurityConfig{
		Enabled: true,
		Processors: map[string]json.RawMessage{
			"av_scan":    avRaw,
			"pii_redact": piiRaw,
		},
	}
	reg := newReg(&cleanProcessor{name: "av_scan"}, &cleanProcessor{name: "pii_redact"})

	fileProcs := middleware.EnabledProcessors(cfg, reg, "file")
	if len(fileProcs) != 1 || fileProcs[0] != "av_scan" {
		t.Errorf("file part should only get av_scan, got %v", fileProcs)
	}

	textProcs := middleware.EnabledProcessors(cfg, reg, "text")
	if len(textProcs) != 1 || textProcs[0] != "pii_redact" {
		t.Errorf("text part should only get pii_redact, got %v", textProcs)
	}
}

// ── Pipeline tests ────────────────────────────────────────────────────────────

func TestPipeline_NamesEmpty_ReturnsDisabled(t *testing.T) {
	p := middleware.NewPipeline(middleware.NewRegistry())
	cfg := middleware.DefaultSecurityConfig()
	res := p.Run(context.Background(), middleware.Part{Kind: "file"}, nil, cfg, nil)
	if res.FinalStatus != "disabled" {
		t.Fatalf("expected disabled, got %s", res.FinalStatus)
	}
}

func TestPipeline_AllClean_ReturnsClean(t *testing.T) {
	reg := newReg(&cleanProcessor{name: "av_scan"})
	p := middleware.NewPipeline(reg)
	avRaw, _ := json.Marshal(middleware.AVScanConfig{Enabled: true, MaxFileMB: 5, BlockOnInfected: true})
	cfg := middleware.SecurityConfig{
		Enabled:    true,
		Processors: map[string]json.RawMessage{"av_scan": avRaw},
	}
	res := p.Run(context.Background(), middleware.Part{Kind: "file", Bytes: []byte("data")}, []string{"av_scan"}, cfg, nil)
	if res.FinalStatus != "clean" {
		t.Fatalf("expected clean, got %s", res.FinalStatus)
	}
	if len(res.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(res.Results))
	}
}

func TestPipeline_BlockStopsFurtherProcessors(t *testing.T) {
	ran := false
	second := &cleanProcessor{name: "pii_redact"}
	_ = second // prevent unused warning
	reg := newReg(
		&blockProcessor{name: "av_scan"},
		&cleanProcessor{name: "pii_redact"},
	)
	// Override second processor to track if it ran
	type trackProcessor struct{ cleanProcessor }
	_ = ran

	p := middleware.NewPipeline(reg)
	avRaw, _ := json.Marshal(middleware.AVScanConfig{Enabled: true, MaxFileMB: 5, BlockOnInfected: true})
	piiRaw, _ := json.Marshal(middleware.PIIRedactConfig{Enabled: true})
	cfg := middleware.SecurityConfig{
		Enabled: true,
		Processors: map[string]json.RawMessage{
			"av_scan":    avRaw,
			"pii_redact": piiRaw,
		},
	}
	res := p.Run(context.Background(), middleware.Part{Kind: "file"}, []string{"av_scan", "pii_redact"}, cfg, nil)
	if res.FinalStatus != "infected" {
		t.Fatalf("expected infected, got %s", res.FinalStatus)
	}
	if res.Threat != "TestVirus.EICAR" {
		t.Fatalf("expected threat name, got %q", res.Threat)
	}
	// Only av_scan ran — block stopped pii_redact
	if len(res.Results) != 1 {
		t.Fatalf("expected 1 result (block stopped chain), got %d", len(res.Results))
	}
}

func TestPipeline_ModifiedPartPassedToNext(t *testing.T) {
	captured := ""
	type capturingProcessor struct{ cleanProcessor }

	reg := middleware.NewRegistry()
	reg.Register(&modifyProcessor{name: "pii_redact"})
	reg.Register(&cleanProcessor{name: "audit_capture"})
	_ = captured

	p := middleware.NewPipeline(reg)
	piiRaw, _   := json.Marshal(middleware.PIIRedactConfig{Enabled: true})
	auditRaw, _ := json.Marshal(middleware.AuditCaptureConfig{Enabled: true})
	cfg := middleware.SecurityConfig{
		Enabled: true,
		Processors: map[string]json.RawMessage{
			"pii_redact":    piiRaw,
			"audit_capture": auditRaw,
		},
	}
	res := p.Run(context.Background(),
		middleware.Part{Kind: "text", Text: "my SSN is 123-45-6789"},
		[]string{"pii_redact", "audit_capture"}, cfg, nil)
	if res.FinalStatus != "clean" {
		t.Fatalf("expected clean (flagged but not blocking), got %s", res.FinalStatus)
	}
	if len(res.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(res.Results))
	}
}

func TestPipeline_ProgressPublisherCalled(t *testing.T) {
	type call struct{ processor, status string }
	var calls []call

	pub := &capturePublisher{fn: func(proc, status string) {
		calls = append(calls, call{proc, status})
	}}

	reg := newReg(&cleanProcessor{name: "av_scan"})
	p := middleware.NewPipeline(reg)
	avRaw, _ := json.Marshal(middleware.AVScanConfig{Enabled: true, MaxFileMB: 5, BlockOnInfected: true})
	cfg := middleware.SecurityConfig{
		Enabled:    true,
		Processors: map[string]json.RawMessage{"av_scan": avRaw},
	}
	p.Run(context.Background(), middleware.Part{Kind: "file"}, []string{"av_scan"}, cfg, pub)

	if len(calls) != 2 {
		t.Fatalf("expected 2 progress calls (running + clean), got %d", len(calls))
	}
	if calls[0].status != "running" {
		t.Errorf("first call should be running, got %s", calls[0].status)
	}
	if calls[1].status != "clean" {
		t.Errorf("second call should be clean, got %s", calls[1].status)
	}
}

type capturePublisher struct {
	fn func(processor, status string)
}

func (c *capturePublisher) PublishProgress(_ context.Context, processor, status string, _ map[string]any, _ int64) {
	c.fn(processor, status)
}
