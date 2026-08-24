// Package transform provides a self-describing, extensible function registry
// for the Transform pipeline node. All functions are implemented here in Go
// and exposed to the browser via the admin API — there are no TypeScript
// reimplementations. This is the single source of truth.
//
// Adding a new function:
//  1. Add a FunctionDef entry in init() in this file.
//  2. Implement the function in functions.go.
//  3. Nothing else changes — the browser picks it up automatically.
package transform

import "fmt"

// Category groups functions in the browser picker.
type Category string

const (
	CategoryString     Category = "string"
	CategoryJSON       Category = "json"
	CategoryValidation Category = "validation"
	CategoryNumeric    Category = "numeric"
	CategoryLLMEra     Category = "llm-era"
	CategoryEncoding   Category = "encoding"
	CategoryDate       Category = "date"
	CategoryConditional Category = "conditional"
)

// ArgDef describes a single named argument a function accepts.
type ArgDef struct {
	Key         string `json:"key"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
	Default     string `json:"default,omitempty"`
}

// Example is a canonical input/output pair used for documentation and testing.
// Both Go tests and the browser Test tab render these examples.
type Example struct {
	In   string            `json:"in"`
	Args map[string]string `json:"args,omitempty"` // args to pass when running this example
	Out  string            `json:"out"`
}

// FunctionDef is the self-describing contract for one transform function.
// It is exposed as-is to the browser via GET /admin/transform-functions.
type FunctionDef struct {
	Name        string    `json:"name"`
	Category    Category  `json:"category"`
	Description string    `json:"description"`
	Args        []ArgDef  `json:"args"`
	Examples    []Example `json:"examples"`
}

// Fn is the executable signature for a transform function.
// input is the resolved value of input_var.
// args contains any extra named arguments from the function step config.
// Returns the output string or an error.
type Fn func(input string, args map[string]string) (string, error)

type entry struct {
	def FunctionDef
	fn  Fn
}

var (
	registry    = map[string]*entry{}
	orderedDefs []FunctionDef // preserved insertion order for the catalog API
)

// register adds a function to the registry. Called from init() only.
func register(def FunctionDef, fn Fn) {
	registry[def.Name] = &entry{def: def, fn: fn}
	orderedDefs = append(orderedDefs, def)
}

// Lookup returns the executable Fn for the given function name.
func Lookup(name string) (Fn, bool) {
	e, ok := registry[name]
	if !ok {
		return nil, false
	}
	return e.fn, true
}

// Catalog returns all registered function definitions in insertion order.
// Used by GET /admin/transform-functions.
func Catalog() []FunctionDef {
	return orderedDefs
}

// CatalogByCategory returns the catalog grouped by category.
// Used by the browser function picker.
func CatalogByCategory() map[Category][]FunctionDef {
	out := map[Category][]FunctionDef{}
	for _, def := range orderedDefs {
		out[def.Category] = append(out[def.Category], def)
	}
	return out
}

// Validate checks that all function names in a chain exist in the registry.
// Called by the compiler when validating an agent definition.
func Validate(chain []FunctionStep) error {
	for i, step := range chain {
		if _, ok := registry[step.Fn]; !ok {
			return fmt.Errorf("step %d: unknown transform function %q", i, step.Fn)
		}
	}
	return nil
}

// FunctionStep is one element in a transform function chain.
// Matches the JSON schema stored in agent definition configs.
type FunctionStep struct {
	Fn        string            `json:"fn"`
	InputVar  string            `json:"input_var"`
	OutputVar string            `json:"output_var"`
	Args      map[string]string `json:"args,omitempty"`
}
