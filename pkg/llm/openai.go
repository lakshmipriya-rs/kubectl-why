// SPDX-License-Identifier: MIT

package llm

import "context"

// OpenAIProvider is a stub for the OpenAI Chat Completions API.
// Full implementation is a future milestone.
type OpenAIProvider struct{ model string }

func NewOpenAI(model string) (*OpenAIProvider, error) {
	return &OpenAIProvider{model: model}, nil
}

func (o *OpenAIProvider) Complete(_ context.Context, _ Request) (*Response, error) {
	return nil, errNotImplemented
}

func (o *OpenAIProvider) Name() string { return "openai:" + o.model }
