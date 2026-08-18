package agentgen

import "fmt"

// AppAgentBinding is the per-application instance of a reusable agent.
// EncryptedCredentials are Fernet ciphertext — NEVER plaintext.
type AppAgentBinding struct {
	ID            string
	ApplicationID string
	AgentID       string
	// DefinitionID is nil while the binding is being drafted.
	// MUST be non-nil once the parent application is published.
	DefinitionID         *string
	// EncryptedCredentials: slot_name → Fernet ciphertext. NEVER plaintext.
	EncryptedCredentials map[string]string
	ConfigOverrides      map[string]any
	Policies             InvocationPolicies
}

// ResolveCredentials decrypts each slot into a request-scoped map.
// The returned map must never be logged or persisted.
func (b AppAgentBinding) ResolveCredentials(dec func(string) (string, error)) (map[string]string, error) {
	out := make(map[string]string, len(b.EncryptedCredentials))
	for slot, ct := range b.EncryptedCredentials {
		pt, err := dec(ct)
		if err != nil {
			return nil, fmt.Errorf("decrypt slot %q: %w", slot, err)
		}
		out[slot] = pt
	}
	return out, nil
}
