package orchestrator

import "testing"

// fakeCostEstimator is a minimal in-package CostEstimator for testing the
// fallback behavior of Orchestrator.estimateCost.
type fakeCostEstimator struct {
	rates map[string]float64 // model -> flat cost regardless of token counts, for assertions
	known map[string]bool
}

func (f fakeCostEstimator) EstimateCost(model string, _, _ int) (float64, bool) {
	if !f.known[model] {
		return 0, false
	}
	return f.rates[model], true
}

// TestEstimateCost_DefaultRateCard_KnownModel verifies the built-in default
// rate card computes a non-zero cost for a known Claude model.
func TestEstimateCost_DefaultRateCard_KnownModel(t *testing.T) {
	cost := estimateCost("claude-sonnet-4-6", 1_000_000, 1_000_000)
	if cost != 3.0+15.0 {
		t.Errorf("want cost=18.0 (3.0 in + 15.0 out per 1M tokens), got %v", cost)
	}
}

// TestEstimateCost_DefaultRateCard_UnknownModelFallsBackToSonnet verifies
// unknown models default to Sonnet pricing rather than erroring or zeroing out.
func TestEstimateCost_DefaultRateCard_UnknownModelFallsBackToSonnet(t *testing.T) {
	got := estimateCost("some-unheard-of-model", 1_000_000, 1_000_000)
	want := estimateCost("claude-sonnet-4-6", 1_000_000, 1_000_000)
	if got != want {
		t.Errorf("unknown model should default to Sonnet pricing: got %v, want %v", got, want)
	}
}

// TestOrchestrator_EstimateCost_PrefersAttachedEstimator verifies that when a
// CostEstimator is attached and has a rate for the model, its value is used
// instead of the built-in default rate card — this is how DB-sourced pricing
// (them.llm_providers.model_pricing) overrides the hardcoded Claude-only table.
func TestOrchestrator_EstimateCost_PrefersAttachedEstimator(t *testing.T) {
	o := New(Config{Model: "gpt-4o"}, nil, nil, nil, nil, nil)
	o.WithCostEstimator(fakeCostEstimator{
		known: map[string]bool{"gpt-4o": true},
		rates: map[string]float64{"gpt-4o": 42.0},
	})

	got := o.estimateCost(1, 1)
	if got != 42.0 {
		t.Errorf("want attached estimator's cost=42.0, got %v", got)
	}
}

// TestOrchestrator_EstimateCost_FallsBackWhenEstimatorHasNoRate verifies that
// when the attached CostEstimator returns ok=false for the model (no DB
// pricing entry), estimateCost falls back to the built-in default rate card
// instead of silently reporting zero cost.
func TestOrchestrator_EstimateCost_FallsBackWhenEstimatorHasNoRate(t *testing.T) {
	o := New(Config{Model: "claude-sonnet-4-6"}, nil, nil, nil, nil, nil)
	o.WithCostEstimator(fakeCostEstimator{known: map[string]bool{}, rates: map[string]float64{}})

	got := o.estimateCost(1_000_000, 1_000_000)
	want := estimateCost("claude-sonnet-4-6", 1_000_000, 1_000_000)
	if got != want {
		t.Errorf("want fallback to default rate card (%v), got %v", want, got)
	}
}

// TestOrchestrator_EstimateCost_NoEstimatorAttached verifies the zero-value
// (no CostEstimator wired) still uses the built-in default rate card —
// existing callers that never call WithCostEstimator see no behavior change.
func TestOrchestrator_EstimateCost_NoEstimatorAttached(t *testing.T) {
	o := New(Config{Model: "claude-haiku-4-5-20251001"}, nil, nil, nil, nil, nil)

	got := o.estimateCost(1_000_000, 1_000_000)
	want := estimateCost("claude-haiku-4-5-20251001", 1_000_000, 1_000_000)
	if got != want {
		t.Errorf("want default rate card (%v) when no estimator attached, got %v", want, got)
	}
}
