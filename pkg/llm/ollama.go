// SPDX-License-Identifier: MIT

package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const defaultOllamaURL = "http://localhost:11434/api/generate"

// OllamaProvider calls a local Ollama instance.
type OllamaProvider struct {
	model  string
	apiURL string
	client *http.Client
}

// NewOllama returns an OllamaProvider targeting localhost:11434.
func NewOllama(model string) *OllamaProvider {
	return &OllamaProvider{
		model:  model,
		apiURL: defaultOllamaURL,
		client: &http.Client{},
	}
}

// NewOllamaWithURL is used in tests to point at a mock server.
func NewOllamaWithURL(model, url string, client *http.Client) *OllamaProvider {
	return &OllamaProvider{model: model, apiURL: url, client: client}
}

func (o *OllamaProvider) Name() string { return "ollama:" + o.model }

type ollamaRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
	Format string `json:"format"`
}

type ollamaResponse struct {
	Response string `json:"response"`
	Done     bool   `json:"done"`
}

func (o *OllamaProvider) Complete(ctx context.Context, req Request) (*Response, error) {
	// Combine system prompt and user message into a single prompt for Ollama.
	combined := req.SystemPrompt + "\n\n---\n\n" + req.UserMessage

	body, _ := json.Marshal(ollamaRequest{
		Model:  o.model,
		Prompt: combined,
		Stream: false,
		Format: "json",
	})

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building ollama request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("calling ollama at %s: %w", o.apiURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("ollama returned HTTP %d: %s", resp.StatusCode, b)
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading ollama response: %w", err)
	}

	var ollamaResp ollamaResponse
	if err := json.Unmarshal(raw, &ollamaResp); err != nil {
		return nil, fmt.Errorf("parsing ollama envelope: %w", err)
	}

	// The model returns a JSON string in ollamaResp.Response.
	var llmResp Response
	if err := json.Unmarshal([]byte(strings.TrimSpace(ollamaResp.Response)), &llmResp); err != nil {
		return nil, fmt.Errorf("parsing LLM JSON response: %w (raw: %.200s)", err, ollamaResp.Response)
	}

	return &llmResp, nil
}
