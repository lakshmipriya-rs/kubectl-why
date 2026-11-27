// SPDX-License-Identifier: MIT

package llm

// Request is the structured payload sent to an LLM provider.
type Request struct {
	SystemPrompt string
	UserMessage  string
}

// Response is the structured output expected from the LLM.
// The LLM is prompted to return JSON matching this shape.
type Response struct {
	RootCause   string   `json:"root_cause"`
	CausalChain []string `json:"causal_chain"`
	// EvidenceIDs lists the IDs (E1, E2, …) of rule-engine evidence items that
	// support RootCause. The validator drops IDs that don't exist.
	EvidenceIDs []string `json:"evidence_ids"`
	Fix         string   `json:"fix"`
	Confidence  string   `json:"confidence"` // "high" | "medium" | "low"
}
