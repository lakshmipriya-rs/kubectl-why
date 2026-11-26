// SPDX-License-Identifier: MIT

package validate_test

import (
	"reflect"
	"testing"

	"github.com/lakshmipriya-rs/kubectl-why/pkg/llm"
	"github.com/lakshmipriya-rs/kubectl-why/pkg/rules"
	"github.com/lakshmipriya-rs/kubectl-why/pkg/validate"
)

var findings = []rules.Finding{{
	Category: rules.CategoryOOMKilled,
	Evidence: []rules.Evidence{
		{ID: "E1", Source: "Pod/p", Field: "f", Value: "OOMKilled"},
		{ID: "E2", Source: "Pod/p", Field: "g", Value: "10Mi"},
	},
}}

func TestFilterUnsupported_DropsUnknownAndDuplicateIDs(t *testing.T) {
	resp := &llm.Response{RootCause: "limit too low", EvidenceIDs: []string{"E2", "E9", "E2", "made-up"}, Confidence: "high"}
	out := validate.FilterUnsupported(resp, findings)
	if !reflect.DeepEqual(out.EvidenceIDs, []string{"E2"}) {
		t.Errorf("EvidenceIDs = %v, want [E2]", out.EvidenceIDs)
	}
	if out.Confidence != "high" {
		t.Errorf("confidence = %q, want high (one valid citation remains)", out.Confidence)
	}
	if len(resp.EvidenceIDs) != 4 {
		t.Error("input response was modified")
	}
}

func TestFilterUnsupported_NoValidCitationLowersConfidence(t *testing.T) {
	resp := &llm.Response{RootCause: "network issue", EvidenceIDs: []string{"E7"}, Confidence: "high"}
	out := validate.FilterUnsupported(resp, findings)
	if len(out.EvidenceIDs) != 0 || out.Confidence != "low" {
		t.Errorf("got ids=%v confidence=%q, want none/low", out.EvidenceIDs, out.Confidence)
	}
}

func TestFilterUnsupported_UncitedClaimWithNoFindings(t *testing.T) {
	out := validate.FilterUnsupported(&llm.Response{RootCause: "guess", Confidence: "medium"}, nil)
	if out.Confidence != "low" {
		t.Errorf("confidence = %q, want low", out.Confidence)
	}
}

func TestFilterUnsupported_EmptyResponse(t *testing.T) {
	out := validate.FilterUnsupported(&llm.Response{}, findings)
	if out.Confidence != "" {
		t.Errorf("empty (--no-llm) response should stay empty, got confidence %q", out.Confidence)
	}
	if validate.FilterUnsupported(nil, findings) == nil {
		t.Error("nil response should become an empty response")
	}
}
