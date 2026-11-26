// SPDX-License-Identifier: MIT

package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewOllama_HasTimeout(t *testing.T) {
	o := NewOllama("llama3")
	if o.client.Timeout <= 0 {
		t.Fatal("default Ollama client must have a timeout so a stalled server cannot hang the CLI")
	}
}

func TestOllama_Complete_ParsesResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner := `{"root_cause":"bad tag","causal_chain":["a","b"],"evidence_ids":["E1"],"fix":"use nginx:1.27","confidence":"high"}`
		_ = json.NewEncoder(w).Encode(map[string]any{"response": inner, "done": true})
	}))
	defer srv.Close()

	o := NewOllamaWithURL("llama3", srv.URL, srv.Client())
	resp, err := o.Complete(context.Background(), Request{SystemPrompt: "s", UserMessage: "u"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.RootCause != "bad tag" || resp.Confidence != "high" || len(resp.CausalChain) != 2 {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestOllama_Complete_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model not found", http.StatusNotFound)
	}))
	defer srv.Close()

	o := NewOllamaWithURL("missing", srv.URL, srv.Client())
	if _, err := o.Complete(context.Background(), Request{}); err == nil {
		t.Fatal("expected error for HTTP 404")
	}
}

func TestOllama_Complete_RespectsContextCancel(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // simulate a stalled model
	}))
	defer srv.Close()
	defer close(release)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	o := NewOllamaWithURL("llama3", srv.URL, srv.Client())
	start := time.Now()
	if _, err := o.Complete(ctx, Request{}); err == nil {
		t.Fatal("expected error when context is cancelled")
	}
	if time.Since(start) > 5*time.Second {
		t.Error("Complete did not return promptly after context cancellation")
	}
}
