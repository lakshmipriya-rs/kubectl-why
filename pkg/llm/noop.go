// SPDX-License-Identifier: MIT

package llm

import "context"

// NoopProvider is used with --no-llm: it returns an empty response immediately.
type NoopProvider struct{}

func NewNoop() *NoopProvider { return &NoopProvider{} }

func (n *NoopProvider) Complete(_ context.Context, _ Request) (*Response, error) {
	return &Response{}, nil
}

func (n *NoopProvider) Name() string { return "noop" }
