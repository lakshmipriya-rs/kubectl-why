// SPDX-License-Identifier: MIT

// Package validate drops LLM claims that have no traceable evidence in the
// collected pod facts, reducing hallucination risk.
package validate

import (
	"strings"

	"github.com/lakshmipriya-rs/kubectl-why/pkg/collect"
	"github.com/lakshmipriya-rs/kubectl-why/pkg/llm"
)

// FilterUnsupported removes evidence items from resp that cannot be corroborated
// by anything in facts. If all evidence items are unsupported, the fix and
// root_cause are blanked and confidence is set to "low".
func FilterUnsupported(resp *llm.Response, facts *collect.PodFacts) *llm.Response {
	if resp == nil {
		return &llm.Response{}
	}
	if facts == nil || facts.Pod == nil {
		return resp
	}

	// Build a corpus of raw strings from the facts to match against.
	corpus := buildCorpus(facts)

	var supported []string
	for _, e := range resp.Evidence {
		if isSupportedByCorpus(e, corpus) {
			supported = append(supported, e)
		}
	}

	out := *resp
	out.Evidence = supported

	// If no evidence survived, downgrade confidence.
	if len(supported) == 0 && len(resp.Evidence) > 0 {
		out.Confidence = "low"
	}

	return &out
}

// buildCorpus assembles searchable text from every raw fact.
func buildCorpus(facts *collect.PodFacts) []string {
	var corpus []string

	if facts.Pod != nil {
		corpus = append(corpus, facts.Pod.Name)
		corpus = append(corpus, string(facts.Pod.Status.Phase))
		for _, cs := range facts.Pod.Status.ContainerStatuses {
			corpus = append(corpus, cs.Name, cs.Image)
			if cs.State.Waiting != nil {
				corpus = append(corpus, cs.State.Waiting.Reason, cs.State.Waiting.Message)
			}
			if cs.State.Terminated != nil {
				corpus = append(corpus, cs.State.Terminated.Reason)
			}
		}
	}

	for _, evt := range facts.Events {
		corpus = append(corpus, evt.Reason, evt.Message)
	}

	for _, log := range facts.CurrentLogs {
		corpus = append(corpus, log)
	}
	for _, log := range facts.PreviousLogs {
		corpus = append(corpus, log)
	}

	return corpus
}

// isSupportedByCorpus returns true if any meaningful word from claim appears in corpus.
func isSupportedByCorpus(claim string, corpus []string) bool {
	// Tokenise claim into significant words (> 3 chars, skip stop words).
	words := tokenize(claim)
	if len(words) == 0 {
		return true // empty claim: don't penalise
	}

	joined := strings.Join(corpus, " ")
	for _, w := range words {
		if strings.Contains(strings.ToLower(joined), strings.ToLower(w)) {
			return true
		}
	}
	return false
}

var stopWords = map[string]bool{
	"the": true, "and": true, "for": true, "are": true, "was": true,
	"with": true, "this": true, "that": true, "from": true, "not": true,
	"has": true, "have": true, "been": true, "its": true, "pod": true,
}

func tokenize(s string) []string {
	var out []string
	for _, w := range strings.Fields(s) {
		w = strings.Trim(strings.ToLower(w), `.,;:"'()[]`)
		if len(w) > 3 && !stopWords[w] {
			out = append(out, w)
		}
	}
	return out
}
