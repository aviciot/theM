package pii_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aviciot/them/internal/middleware"
	"github.com/aviciot/them/internal/middleware/pii"
)

// PII-1: disabled config is a no-op skip, regardless of content.
func TestDetector_Disabled_Skips(t *testing.T) {
	d := pii.New()
	cfg, _ := json.Marshal(middleware.PIIRedactConfig{Enabled: false})
	r, err := d.Process(context.Background(), middleware.Part{Kind: "text", Text: "email me at a@b.com"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Outcome != "skipped" {
		t.Errorf("Outcome = %q, want skipped", r.Outcome)
	}
}

// PII-2: non-text part kinds are always skipped, even when enabled.
func TestDetector_NonTextPart_Skips(t *testing.T) {
	d := pii.New()
	cfg, _ := json.Marshal(middleware.PIIRedactConfig{Enabled: true, Mode: "redact"})
	r, err := d.Process(context.Background(), middleware.Part{Kind: "file", Bytes: []byte("a@b.com")}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Outcome != "skipped" {
		t.Errorf("Outcome = %q, want skipped", r.Outcome)
	}
}

// PII-3: clean text (no PII) reports Outcome=clean, Modified=nil.
func TestDetector_NoMatch_Clean(t *testing.T) {
	d := pii.New()
	cfg, _ := json.Marshal(middleware.PIIRedactConfig{Enabled: true, Mode: "redact"})
	r, err := d.Process(context.Background(), middleware.Part{Kind: "text", Text: "the weather is nice today"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Outcome != "clean" {
		t.Errorf("Outcome = %q, want clean", r.Outcome)
	}
	if r.Modified != nil {
		t.Error("Modified should be nil for clean text")
	}
}

// PII-4: mode=redact masks the matched email in place and reports the category.
func TestDetector_ModeRedact_MasksInPlace(t *testing.T) {
	d := pii.New()
	cfg, _ := json.Marshal(middleware.PIIRedactConfig{Enabled: true, Mode: "redact"})
	r, err := d.Process(context.Background(), middleware.Part{Kind: "text", Text: "contact me at jane.doe@example.com please"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Outcome != "flagged" {
		t.Fatalf("Outcome = %q, want flagged", r.Outcome)
	}
	if r.Block {
		t.Error("Block should be false in redact mode")
	}
	if r.Modified == nil {
		t.Fatal("Modified should be set in redact mode")
	}
	if r.Modified.Text == "contact me at jane.doe@example.com please" {
		t.Error("email was not redacted")
	}
	cats, ok := r.Detail["categories"].(map[string]int)
	if !ok || cats["email"] != 1 {
		t.Errorf("Detail categories = %+v, want email:1", r.Detail)
	}
}

// PII-5: mode=block reports Block=true and does not modify the text (the
// caller rejects the whole response, redaction is irrelevant).
func TestDetector_ModeBlock_SetsBlockTrue(t *testing.T) {
	d := pii.New()
	cfg, _ := json.Marshal(middleware.PIIRedactConfig{Enabled: true, Mode: "block"})
	r, err := d.Process(context.Background(), middleware.Part{Kind: "text", Text: "ssn: 123-45-6789"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !r.Block {
		t.Error("Block should be true in block mode when PII is found")
	}
}

// PII-6: mode=warn flags but never blocks and never modifies.
func TestDetector_ModeWarn_FlagsOnly(t *testing.T) {
	d := pii.New()
	cfg, _ := json.Marshal(middleware.PIIRedactConfig{Enabled: true, Mode: "warn"})
	r, err := d.Process(context.Background(), middleware.Part{Kind: "text", Text: "call 555-123-4567"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Outcome != "flagged" {
		t.Errorf("Outcome = %q, want flagged", r.Outcome)
	}
	if r.Block {
		t.Error("Block should be false in warn mode")
	}
	if r.Modified != nil {
		t.Error("Modified should be nil in warn mode")
	}
}

// PII-7: empty mode defaults to redact (same fail-open-to-safest convention
// documented for other guard configs in this codebase).
func TestDetector_EmptyMode_DefaultsToRedact(t *testing.T) {
	d := pii.New()
	cfg, _ := json.Marshal(middleware.PIIRedactConfig{Enabled: true})
	r, err := d.Process(context.Background(), middleware.Part{Kind: "text", Text: "a@b.com"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Modified == nil {
		t.Error("empty mode should default to redact and set Modified")
	}
}

// PII-8: empty Categories means "scan everything" — fail-open to scanning
// more, not less. A text with both an email and a phone number must flag
// both categories when Categories is unset.
func TestDetector_EmptyCategories_ScansEverything(t *testing.T) {
	d := pii.New()
	cfg, _ := json.Marshal(middleware.PIIRedactConfig{Enabled: true, Mode: "warn"})
	r, err := d.Process(context.Background(), middleware.Part{Kind: "text", Text: "email a@b.com or call 555-123-4567"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cats, ok := r.Detail["categories"].(map[string]int)
	if !ok || cats["email"] != 1 || cats["phone"] != 1 {
		t.Errorf("Detail categories = %+v, want both email and phone flagged", r.Detail)
	}
}

// PII-9: a Categories list restricts detection to only the named
// categories — a credit-card-shaped number must NOT be flagged when
// Categories only lists "email".
func TestDetector_CategoriesRestrictsToNamedOnly(t *testing.T) {
	d := pii.New()
	cfg, _ := json.Marshal(middleware.PIIRedactConfig{Enabled: true, Mode: "warn", Categories: []string{"email"}})
	r, err := d.Process(context.Background(), middleware.Part{Kind: "text", Text: "card 4111111111111111, email a@b.com"}, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cats, ok := r.Detail["categories"].(map[string]int)
	if !ok {
		t.Fatalf("Detail = %+v, want categories present (email should still match)", r.Detail)
	}
	if _, hasCard := cats["credit_card"]; hasCard {
		t.Error("credit_card should not be checked when Categories only lists \"email\"")
	}
	if cats["email"] != 1 {
		t.Error("email should still be checked and matched")
	}
}

// PII-10: CategoryNames returns the fixed, real category name set — used
// by the App Canvas Guards UI to build its checklist. Pins the exact names
// so a rename in pii.go is a deliberate, visible test change, not a silent
// UI/config mismatch.
func TestCategoryNames_ReturnsFixedSet(t *testing.T) {
	got := pii.CategoryNames()
	want := []string{"email", "phone", "credit_card", "ssn"}
	if len(got) != len(want) {
		t.Fatalf("CategoryNames() = %v, want %v", got, want)
	}
	for i, name := range want {
		if got[i] != name {
			t.Errorf("CategoryNames()[%d] = %q, want %q", i, got[i], name)
		}
	}
}
