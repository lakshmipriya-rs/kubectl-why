// SPDX-License-Identifier: MIT

package llm

import (
	"context"
	"errors"
)

// errNotImplemented is returned by stub providers.
var errNotImplemented = errors.New("provider not yet implemented; use --provider ollama or --no-llm")

// AnthropicProvider is a stub for the Anthropic Messages API.
// Full implementation is a future milestone.
type AnthropicProvider struct{ model string }

func NewAnthropic(model string) (*AnthropicProvider, error) {
	return &AnthropicProvider{model: model}, nil
}

func (a *AnthropicProvider) Complete(_ context.Context, _ Request) (*Response, error) {
	return nil, errNotImplemented
}

func (a *AnthropicProvider) Name() string { return "anthropic:" + a.model }
