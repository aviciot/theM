package appflow

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// AF-IN-01: {{.input}} and {{.summary}} interpolate against flow vars.
func TestRenderFlowTemplate_Substitution(t *testing.T) {
	vars := FlowVars{
		"input":   "hello",
		"summary": "world",
	}
	out, err := renderFlowTemplate("{{.input}} {{.summary}}", vars)
	require.NoError(t, err)
	assert.Equal(t, "hello world", out)
}

// AF-IN-02: a missing key does NOT return an error. Verified actual behavior
// by running the test rather than assuming it: with missingkey=zero over a
// map[string]string (FlowVars), text/template substitutes the zero value of
// the map's value type — "" for string — not the literal "<no value>" text.
// "<no value>" is what missingkey=zero produces for map[string]interface{} or
// struct field lookups, where the zero value is untyped/interface and
// text/template falls back to that sentinel string. isTruthy still treats
// both "" and "<no value>" as false, so branch semantics are unaffected.
func TestRenderFlowTemplate_MissingVar(t *testing.T) {
	vars := FlowVars{
		"input": "hello",
	}
	out, err := renderFlowTemplate("{{.input}} {{.missing}}", vars)
	require.NoError(t, err)
	assert.Equal(t, "hello ", out)
}

// AF-IN-02b: the condition expression forms the properties panel advertises as
// examples must actually evaluate. Guards against shipping UI hints that fail
// at run time, and pins the missing-variable path to the false branch.
func TestRenderFlowTemplate_ConditionExpressionForms(t *testing.T) {
	vars := FlowVars{"output": "APPROVED", "summary": "all good"}

	cases := []struct {
		name string
		tmpl string
		want bool
	}{
		{"eq match", `{{eq .output "APPROVED"}}`, true},
		{"eq no match", `{{eq .output "REJECTED"}}`, false},
		{"len gt", `{{gt (len .output) 3}}`, true},
		{"len gt false", `{{gt (len .output) 100}}`, false},
		{"contains", `{{contains .summary "good"}}`, true},
		{"contains false", `{{contains .summary "bad"}}`, false},
		{"hasPrefix", `{{hasPrefix .output "APP"}}`, true},
		{"lower+eq", `{{eq (lower .output) "approved"}}`, true},
		{"missing var eq", `{{eq .nope "x"}}`, false},
		{"missing var len", `{{gt (len .nope) 0}}`, false},
		{"missing var contains", `{{contains .nope "x"}}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := renderFlowTemplate(tc.tmpl, vars)
			require.NoError(t, err, "template %q must render", tc.tmpl)
			assert.Equal(t, tc.want, isTruthy(out), "template %q rendered %q", tc.tmpl, out)
		})
	}
}

// AF-IN-03: unbalanced "{{" is a parse error.
func TestRenderFlowTemplate_ParseError(t *testing.T) {
	_, err := renderFlowTemplate("{{.input", FlowVars{"input": "hello"})
	require.Error(t, err)
}

// AF-IN-04: table-driven truthiness per the missingkey=zero / branch semantics.
func TestIsTruthy(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"true", "true", true},
		{"TRUE", "TRUE", true},
		{"1", "1", true},
		{"x", "x", true},
		{"empty", "", false},
		{"false", "false", false},
		{"zero", "0", false},
		{"no value", "<no value>", false},
		{"whitespace only", "   ", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isTruthy(tc.in))
		})
	}
}

// AF-IN-05: all InlineLLMConfig fields, including *float64 Temperature,
// survive a marshal/unmarshal round trip.
func TestInlineLLMConfig_JSONRoundTrip(t *testing.T) {
	temp := 0.7
	cfg := InlineLLMConfig{
		Provider:     "anthropic",
		Model:        "claude-sonnet",
		SystemPrompt: "you are helpful",
		UserPrompt:   "{{.input}}",
		MaxTokens:    2048,
		Temperature:  &temp,
		OutputVar:    "summary",
	}

	raw, err := json.Marshal(cfg)
	require.NoError(t, err)

	var got InlineLLMConfig
	require.NoError(t, json.Unmarshal(raw, &got))

	assert.Equal(t, cfg.Provider, got.Provider)
	assert.Equal(t, cfg.Model, got.Model)
	assert.Equal(t, cfg.SystemPrompt, got.SystemPrompt)
	assert.Equal(t, cfg.UserPrompt, got.UserPrompt)
	assert.Equal(t, cfg.MaxTokens, got.MaxTokens)
	require.NotNil(t, got.Temperature)
	assert.Equal(t, *cfg.Temperature, *got.Temperature)
	assert.Equal(t, cfg.OutputVar, got.OutputVar)
}

// AF-IN-06: InlineConditionConfig's Expression survives a marshal/unmarshal
// round trip.
func TestInlineConditionConfig_JSONRoundTrip(t *testing.T) {
	cfg := InlineConditionConfig{
		Expression: "{{.score}}",
	}

	raw, err := json.Marshal(cfg)
	require.NoError(t, err)

	var got InlineConditionConfig
	require.NoError(t, json.Unmarshal(raw, &got))

	assert.Equal(t, cfg.Expression, got.Expression)
}
