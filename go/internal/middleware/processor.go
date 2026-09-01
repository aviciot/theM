// Package middleware implements the A2A artifact middleware pipeline.
// Processors implement the Processor interface; the Pipeline chains them in order.
package middleware

import (
	"context"
	"encoding/json"
)

// Part is the artifact content passed through the pipeline.
// Exactly one of Bytes / Text / Data is non-empty depending on Kind.
type Part struct {
	Kind     string // "file" | "text" | "data"
	Bytes    []byte // file content
	Text     string // text content
	Data     []byte // JSON bytes for data parts
	MimeType string
	FileName string
}

// Result is the outcome of one Processor.Process call.
type Result struct {
	Outcome    string         // "clean" | "infected" | "flagged" | "error" | "skipped"
	Modified   *Part          // non-nil if the processor modified the part in place
	Block      bool           // stop the pipeline and tombstone the artifact
	Detail     map[string]any // threat name, PII categories, etc.
	DurationMS int64
}

// Processor is implemented by each middleware (av_scan, pii_redact, etc.).
type Processor interface {
	Name() string
	// Process runs the processor on part using the JSON config blob for this processor.
	// Returns Result. An error means the processor itself failed (not that content was bad).
	Process(ctx context.Context, part Part, cfg json.RawMessage) (Result, error)
}

// Registry holds all registered processors by name.
type Registry struct {
	procs map[string]Processor
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{procs: make(map[string]Processor)}
}

// Register adds a Processor. Panics on duplicate name.
func (r *Registry) Register(p Processor) {
	if _, exists := r.procs[p.Name()]; exists {
		panic("middleware: duplicate processor: " + p.Name())
	}
	r.procs[p.Name()] = p
}

// Get returns the Processor for name, or nil if not registered.
func (r *Registry) Get(name string) Processor {
	return r.procs[name]
}

// Names returns all registered processor names in registration order.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.procs))
	for n := range r.procs {
		names = append(names, n)
	}
	return names
}
