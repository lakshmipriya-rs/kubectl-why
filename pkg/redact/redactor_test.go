// SPDX-License-Identifier: MIT

package redact_test

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/lakshmipriya-rs/kubectl-why/pkg/collect"
	"github.com/lakshmipriya-rs/kubectl-why/pkg/redact"
	"github.com/lakshmipriya-rs/kubectl-why/pkg/rules"
)

func TestRedact_NilFacts(t *testing.T) {
	r := redact.Redact(nil)
	if r == nil {
		t.Error("Redact(nil) should return empty struct, not nil")
	}
}

func TestRedact_BasicFields(t *testing.T) {
	facts := &collect.PodFacts{
		Pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "mypod", Namespace: "prod"},
			Status:     corev1.PodStatus{Phase: corev1.PodRunning},
		},
		CurrentLogs:  map[string]string{"app": "hello world"},
		PreviousLogs: map[string]string{},
	}
	r := redact.Redact(facts)
	if r.PodName != "mypod" {
		t.Errorf("PodName = %q", r.PodName)
	}
	if r.Namespace != "prod" {
		t.Errorf("Namespace = %q", r.Namespace)
	}
}

var secretPatterns = []struct {
	name  string
	input string
}{
	{"AWS key", "AKIAIOSFODNN7EXAMPLE"},
	{"Bearer token", "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.payload.sig"},
	{"password assignment", "DB_PASSWORD=s3cr3tV@lue"},
	{"OpenAI key", "sk-abcdefghijklmnopqrstuvwxyz12345"},
}

func TestRedact_SecretsStripped(t *testing.T) {
	for _, tc := range secretPatterns {
		t.Run(tc.name, func(t *testing.T) {
			facts := &collect.PodFacts{
				Pod: &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: "p"},
					Status:     corev1.PodStatus{Phase: corev1.PodRunning},
				},
				CurrentLogs:  map[string]string{"app": tc.input},
				PreviousLogs: map[string]string{},
			}
			r := redact.Redact(facts)
			for _, log := range r.CurrentLogs {
				if strings.Contains(log, tc.input) {
					t.Errorf("secret not redacted in log: %q", log)
				}
			}
		})
	}
}

func TestRedact_SecretNamesPreserved(t *testing.T) {
	facts := &collect.PodFacts{
		Pod: &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "p"},
			Status:     corev1.PodStatus{Phase: corev1.PodRunning},
		},
		RelatedSecrets:    []string{"db-credentials"},
		RelatedConfigMaps: []string{"app-config"},
		CurrentLogs:       map[string]string{},
		PreviousLogs:      map[string]string{},
	}
	r := redact.Redact(facts)
	if len(r.RelatedSecrets) != 1 || r.RelatedSecrets[0] != "db-credentials" {
		t.Error("secret name should be preserved")
	}
	if len(r.RelatedCMs) != 1 || r.RelatedCMs[0] != "app-config" {
		t.Error("CM name should be preserved")
	}
}

// Rule evidence contains raw event/container messages and must be redacted
// before it is sent to an LLM or printed by --dry-run.
func TestFindings_EvidenceRedacted(t *testing.T) {
	const secret = "DB_PASSWORD=s3cr3tV@lue"
	in := []rules.Finding{{
		Category: rules.CategoryImagePullBackOff,
		Detail:   "detail",
		Evidence: []string{`event "Failed": ` + secret, `image: "nginx:1.27"`},
	}}
	out := redact.Findings(in)
	if len(out) != 1 {
		t.Fatalf("got %d findings, want 1", len(out))
	}
	for _, e := range out[0].Evidence {
		if strings.Contains(e, "s3cr3tV@lue") {
			t.Errorf("secret not redacted in evidence: %q", e)
		}
	}
	if out[0].Evidence[1] != `image: "nginx:1.27"` {
		t.Errorf("harmless evidence changed: %q", out[0].Evidence[1])
	}
	// The caller's findings (used for the local report) must be untouched.
	if !strings.Contains(in[0].Evidence[0], secret) {
		t.Error("Findings modified its input slice")
	}
}

func TestFindings_Nil(t *testing.T) {
	if out := redact.Findings(nil); out != nil {
		t.Errorf("Findings(nil) = %v, want nil", out)
	}
}
