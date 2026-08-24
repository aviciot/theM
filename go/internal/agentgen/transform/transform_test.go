package transform_test

import (
	"testing"

	"github.com/aviciot/them/internal/agentgen/transform"
)

// TestCatalog verifies the catalog is non-empty and all entries have required fields.
func TestCatalog(t *testing.T) {
	cat := transform.Catalog()
	if len(cat) == 0 {
		t.Fatal("catalog is empty")
	}
	for _, def := range cat {
		if def.Name == "" {
			t.Errorf("function def missing name: %+v", def)
		}
		if def.Category == "" {
			t.Errorf("function %q missing category", def.Name)
		}
		if def.Description == "" {
			t.Errorf("function %q missing description", def.Name)
		}
		if _, ok := transform.Lookup(def.Name); !ok {
			t.Errorf("function %q in catalog but not in registry", def.Name)
		}
	}
}

// TestFunctionExamples runs every registered function against its own declared examples.
// This is the canonical parity contract: if the example passes here, the browser
// Test tab (which calls this same Go code) will match production.
func TestFunctionExamples(t *testing.T) {
	for _, def := range transform.Catalog() {
		def := def
		t.Run(def.Name, func(t *testing.T) {
			fn, ok := transform.Lookup(def.Name)
			if !ok {
				t.Fatalf("function %q not found in registry", def.Name)
			}
			for i, ex := range def.Examples {
				out, err := fn(ex.In, ex.Args)
				if err != nil {
					t.Errorf("example %d: unexpected error: %v", i, err)
					continue
				}
				if out != ex.Out {
					t.Errorf("example %d:\n  in:   %q\n  want: %q\n  got:  %q", i, ex.In, ex.Out, out)
				}
			}
		})
	}
}

// TestExecute_Chain verifies a multi-step chain produces the correct trace.
func TestExecute_Chain(t *testing.T) {
	chain := []transform.FunctionStep{
		{Fn: "strip_fences", InputVar: "raw", OutputVar: "clean"},
		{Fn: "json_path", InputVar: "clean", OutputVar: "city", Args: map[string]string{"path": "$.city1"}},
		{Fn: "upper", InputVar: "city", OutputVar: "city_up"},
		{Fn: "concat", InputVar: "city_up", OutputVar: "label", Args: map[string]string{"prefix": "City: "}},
	}

	vars := transform.Vars{
		"raw": "```json\n{\"city1\":\"tel aviv\"}\n```",
	}

	trace, err := transform.Execute(chain, vars)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(trace.Steps) != 4 {
		t.Fatalf("expected 4 steps, got %d", len(trace.Steps))
	}
	for i, step := range trace.Steps {
		if !step.OK {
			t.Errorf("step %d (%s) failed: %s", i, step.Fn, step.Error)
		}
	}
	if got, want := vars["label"], "City: TEL AVIV"; got != want {
		t.Errorf("final label: got %q, want %q", got, want)
	}
}

// TestExecute_StopsOnError verifies the chain stops at the first failing step.
func TestExecute_StopsOnError(t *testing.T) {
	chain := []transform.FunctionStep{
		{Fn: "parse_json", InputVar: "bad", OutputVar: "parsed"},
		{Fn: "upper", InputVar: "parsed", OutputVar: "up"},
	}
	vars := transform.Vars{"bad": "not-json"}

	trace, err := transform.Execute(chain, vars)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if len(trace.Steps) != 1 {
		t.Errorf("expected 1 step in trace (chain stopped), got %d", len(trace.Steps))
	}
	if trace.Steps[0].OK {
		t.Error("expected first step to be not-OK")
	}
}

// TestValidate rejects unknown function names.
func TestValidate(t *testing.T) {
	chain := []transform.FunctionStep{
		{Fn: "upper", InputVar: "x", OutputVar: "y"},
		{Fn: "nonexistent_fn", InputVar: "y", OutputVar: "z"},
	}
	if err := transform.Validate(chain); err == nil {
		t.Error("expected validation error for unknown function, got nil")
	}
}

// TestStripFences covers the main LLM-era use case.
func TestStripFences(t *testing.T) {
	cases := []struct{ in, want string }{
		{"```json\n{\"a\":1}\n```", `{"a":1}`},
		{"```\nhello\n```", "hello"},
		{"```javascript\nconsole.log(1)\n```", "console.log(1)"},
		{"no fences here", "no fences here"},
		{"  ```json\n{}\n```  ", "{}"},
	}
	fn, _ := transform.Lookup("strip_fences")
	for _, c := range cases {
		got, err := fn(c.in, nil)
		if err != nil {
			t.Errorf("strip_fences(%q): unexpected error: %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("strip_fences(%q):\n  want %q\n  got  %q", c.in, c.want, got)
		}
	}
}

// TestJSONPath covers nested and missing key cases.
func TestJSONPath(t *testing.T) {
	fn, _ := transform.Lookup("json_path")
	args := map[string]string{"path": "$.city1"}

	got, err := fn(`{"city1":"Rome","city1_lat":"41.9"}`, args)
	if err != nil || got != "Rome" {
		t.Errorf("json_path: got %q, err %v", got, err)
	}

	_, err = fn(`{"a":1}`, map[string]string{"path": "$.missing"})
	if err == nil {
		t.Error("expected error for missing key")
	}
}

// TestDurationTracked verifies timing is recorded per step.
func TestDurationTracked(t *testing.T) {
	chain := []transform.FunctionStep{
		{Fn: "trim", InputVar: "x", OutputVar: "y"},
	}
	vars := transform.Vars{"x": "  hello  "}
	trace, _ := transform.Execute(chain, vars)
	if trace.Steps[0].DurationNs <= 0 {
		t.Error("expected positive duration_ns")
	}
}
