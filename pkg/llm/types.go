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
	Evidence    []string `json:"evidence"`
	Fix         string   `json:"fix"`
	Confidence  string   `json:"confidence"` // "high" | "medium" | "low"
}
