package orchestrator

// estimateCost returns the USD cost for one LLM call based on model pricing.
// Prices are per-token (not per-million) from Anthropic's published rate card.
func estimateCost(model string, inputTokens, outputTokens int) float64 {
	type pricing struct{ in, out float64 }
	// USD per token (divide published $/1M by 1,000,000).
	rates := map[string]pricing{
		"claude-opus-4-5":            {in: 15.0 / 1e6, out: 75.0 / 1e6},
		"claude-opus-4-8":            {in: 15.0 / 1e6, out: 75.0 / 1e6},
		"claude-sonnet-4-5":          {in: 3.0 / 1e6, out: 15.0 / 1e6},
		"claude-sonnet-4-6":          {in: 3.0 / 1e6, out: 15.0 / 1e6},
		"claude-haiku-4-5":           {in: 0.8 / 1e6, out: 4.0 / 1e6},
		"claude-haiku-4-5-20251001":  {in: 0.8 / 1e6, out: 4.0 / 1e6},
		"claude-3-5-sonnet-20241022": {in: 3.0 / 1e6, out: 15.0 / 1e6},
		"claude-3-5-haiku-20241022":  {in: 0.8 / 1e6, out: 4.0 / 1e6},
		"claude-3-opus-20240229":     {in: 15.0 / 1e6, out: 75.0 / 1e6},
	}
	p, ok := rates[model]
	if !ok {
		// Default to Sonnet pricing for unknown models.
		p = pricing{in: 3.0 / 1e6, out: 15.0 / 1e6}
	}
	return p.in*float64(inputTokens) + p.out*float64(outputTokens)
}
