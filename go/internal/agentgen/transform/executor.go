package transform

import (
	"fmt"
	"time"
)

// StepResult is the trace output for one function step execution.
type StepResult struct {
	Fn        string        `json:"fn"`
	InputVar  string        `json:"input_var"`
	OutputVar string        `json:"output_var"`
	In        string        `json:"in"`
	Out       string        `json:"out,omitempty"`
	Error     string        `json:"error,omitempty"`
	OK        bool          `json:"ok"`
	DurationNs int64        `json:"duration_ns"`
}

// TraceResult is the full output of Execute — one StepResult per function step.
type TraceResult struct {
	Steps []StepResult `json:"steps"`
}

// Vars is an alias for the pipeline variable map used during execution.
type Vars map[string]any

// Execute runs a function chain against the provided vars.
// It mutates vars in place (each step's output_var is written back).
// It always returns a TraceResult — on error the failing step has OK=false
// and the chain stops at that step.
func Execute(chain []FunctionStep, vars Vars) (*TraceResult, error) {
	trace := &TraceResult{Steps: make([]StepResult, 0, len(chain))}

	for _, step := range chain {
		fn, ok := Lookup(step.Fn)
		if !ok {
			err := fmt.Errorf("unknown function %q", step.Fn)
			trace.Steps = append(trace.Steps, StepResult{
				Fn:        step.Fn,
				InputVar:  step.InputVar,
				OutputVar: step.OutputVar,
				Error:     err.Error(),
				OK:        false,
			})
			return trace, err
		}

		// Resolve input value from vars.
		inputVal := ""
		if v, exists := vars[step.InputVar]; exists {
			switch s := v.(type) {
			case string:
				inputVal = s
			default:
				inputVal = fmt.Sprintf("%v", v)
			}
		}

		start := time.Now()
		out, err := fn(inputVal, step.Args)
		elapsed := time.Since(start)

		if err != nil {
			trace.Steps = append(trace.Steps, StepResult{
				Fn:         step.Fn,
				InputVar:   step.InputVar,
				OutputVar:  step.OutputVar,
				In:         inputVal,
				Error:      err.Error(),
				OK:         false,
				DurationNs: elapsed.Nanoseconds(),
			})
			return trace, fmt.Errorf("transform step %q: %w", step.Fn, err)
		}

		// Write output to vars so subsequent steps can read it.
		vars[step.OutputVar] = out

		trace.Steps = append(trace.Steps, StepResult{
			Fn:         step.Fn,
			InputVar:   step.InputVar,
			OutputVar:  step.OutputVar,
			In:         inputVal,
			Out:        out,
			OK:         true,
			DurationNs: elapsed.Nanoseconds(),
		})
	}

	return trace, nil
}
