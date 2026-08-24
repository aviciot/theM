// Package transform — custom function placeholder.
//
// This file is reserved for user-defined custom transform functions.
// Custom functions allow escape-hatch string manipulation beyond the built-in
// catalog without requiring a full code execution sandbox.
//
// Planned design (Phase 3):
//   - Admin defines a named custom function with a Go snippet
//   - Snippet is validated at save time (compile check)
//   - Registered at agent load time alongside built-ins
//   - Callable from function chains like any built-in
//
// NOT IMPLEMENTED YET. Do not add code here until the custom function
// security model and sandboxing strategy have been reviewed.
package transform
