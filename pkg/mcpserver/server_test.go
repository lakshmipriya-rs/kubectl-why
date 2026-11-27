// SPDX-License-Identifier: MIT

package mcpserver_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/lakshmipriya-rs/kubectl-why/pkg/mcpserver"
)

// connect starts the server on an in-memory transport and returns a client session.
func connect(t *testing.T, opts mcpserver.Options, objs ...runtime.Object) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	opts.Client = fake.NewSimpleClientset(objs...)
	if opts.DefaultNamespace == "" {
		opts.DefaultNamespace = "shop"
	}
	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := mcpserver.New(opts).Connect(ctx, serverT, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// call invokes a tool and decodes its structured output into out.
func call(t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any, out any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", tool, err)
	}
	if out != nil && !res.IsError {
		raw, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("decoding %s output: %v", tool, err)
		}
	}
	return res
}

func text(res *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

func pod(name, ns string, status corev1.PodStatus) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: map[string]string{"app": "web"}},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}},
		Status:     status,
	}
}

func waiting(reason, msg string) corev1.PodStatus {
	return corev1.PodStatus{Phase: corev1.PodPending, ContainerStatuses: []corev1.ContainerStatus{{
		Name:  "app",
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: reason, Message: msg}},
	}}}
}

func healthy() corev1.PodStatus {
	return corev1.PodStatus{
		Phase:      corev1.PodRunning,
		Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		ContainerStatuses: []corev1.ContainerStatus{{
			Name: "app", Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
		}},
	}
}

func TestListTools_AllReadOnly(t *testing.T) {
	cs := connect(t, mcpserver.Options{})
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range res.Tools {
		names[tool.Name] = true
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("tool %s must be annotated read-only", tool.Name)
		}
		if tool.InputSchema == nil || tool.OutputSchema == nil {
			t.Errorf("tool %s should have input and output schemas", tool.Name)
		}
	}
	for _, want := range []string{"diagnose", "list_unhealthy", "get_events"} {
		if !names[want] {
			t.Errorf("missing tool %s (have %v)", want, names)
		}
	}
}

func TestDiagnose(t *testing.T) {
	cs := connect(t, mcpserver.Options{},
		pod("web-1", "shop", waiting("CreateContainerConfigError", `secret "db-creds" not found; token=abcd1234secret`)))

	var out mcpserver.DiagnoseOutput
	res := call(t, cs, "diagnose", map[string]any{"target": "web-1"}, &out)
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", text(res))
	}
	if out.Namespace != "shop" || out.Target.Pod != "web-1" || out.Healthy {
		t.Errorf("unexpected output: %+v", out)
	}
	if len(out.Findings) != 1 || out.Findings[0].Category != "MissingSecret" {
		t.Fatalf("findings = %+v, want one MissingSecret", out.Findings)
	}
	if out.Findings[0].Evidence[0].ID != "E1" {
		t.Errorf("evidence IDs missing: %+v", out.Findings[0].Evidence)
	}
	// Redaction applies to both the structured output and the text.
	raw, _ := json.Marshal(res.StructuredContent)
	if strings.Contains(string(raw), "abcd1234secret") || strings.Contains(text(res), "abcd1234secret") {
		t.Error("secret leaked into the tool result")
	}
	if !strings.Contains(text(res), "[MissingSecret]") || !strings.Contains(text(res), "untrusted data") {
		t.Errorf("text should contain the report and the untrusted-data note:\n%s", text(res))
	}
}

func TestDiagnose_HealthyAndErrors(t *testing.T) {
	cs := connect(t, mcpserver.Options{}, pod("ok", "shop", healthy()))

	var out mcpserver.DiagnoseOutput
	if res := call(t, cs, "diagnose", map[string]any{"target": "ok"}, &out); res.IsError || !out.Healthy {
		t.Errorf("healthy pod: got %+v (%s)", out, text(res))
	}
	if res := call(t, cs, "diagnose", map[string]any{"target": "ghost"}, nil); !res.IsError {
		t.Error("missing pod should be a tool error")
	}
	if res := call(t, cs, "diagnose", map[string]any{"target": "ingress/x"}, nil); !res.IsError ||
		!strings.Contains(text(res), "supported") {
		t.Errorf("unsupported kind should explain what is supported, got %q", text(res))
	}
}

func TestAllowedNamespaces(t *testing.T) {
	cs := connect(t, mcpserver.Options{AllowedNamespaces: []string{"shop"}},
		pod("web-1", "shop", healthy()), pod("db-0", "secrets", healthy()))

	for tool, args := range map[string]map[string]any{
		"diagnose":       {"target": "db-0", "namespace": "secrets"},
		"list_unhealthy": {"namespace": "secrets"},
		"get_events":     {"namespace": "secrets"},
	} {
		res := call(t, cs, tool, args, nil)
		if !res.IsError || !strings.Contains(text(res), `namespace "secrets" is not allowed`) {
			t.Errorf("%s: expected namespace refusal, got %q", tool, text(res))
		}
	}
	if res := call(t, cs, "list_unhealthy", map[string]any{"namespace": "shop"}, nil); res.IsError {
		t.Errorf("allowed namespace refused: %s", text(res))
	}
}

func TestListUnhealthy(t *testing.T) {
	isController := true
	crashing := pod("web-2", "shop", corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{
		Name: "app", RestartCount: 7,
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
	}}})
	crashing.OwnerReferences = []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "web-7d9f", Controller: &isController}}
	cs := connect(t, mcpserver.Options{},
		pod("web-1", "shop", healthy()),
		crashing,
		pod("web-3", "shop", waiting("ImagePullBackOff", "")),
		pod("elsewhere", "other", waiting("ErrImagePull", "")),
	)

	var out mcpserver.ListUnhealthyOutput
	call(t, cs, "list_unhealthy", nil, &out)
	if out.Namespace != "shop" || out.TotalPods != 3 || len(out.Unhealthy) != 2 {
		t.Fatalf("unexpected output: %+v", out)
	}
	first := out.Unhealthy[0] // most restarts first among equally failing pods
	if first.Name != "web-2" || first.Reason != "CrashLoopBackOff" || first.Restarts != 7 ||
		first.Ready != "0/1" || first.Owner != "ReplicaSet/web-7d9f" {
		t.Errorf("unexpected first entry: %+v", first)
	}
	if out.Unhealthy[1].Name != "web-3" || out.Unhealthy[1].Reason != "ImagePullBackOff" {
		t.Errorf("unexpected second entry: %+v", out.Unhealthy[1])
	}
}

func TestGetEvents(t *testing.T) {
	ev := func(name, kind, obj, typ, reason, msg string, ago time.Duration) *corev1.Event {
		return &corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{Name: name, Namespace: "shop"},
			InvolvedObject: corev1.ObjectReference{Kind: kind, Name: obj},
			Type:           typ, Reason: reason, Message: msg, Count: 2,
			LastTimestamp: metav1.NewTime(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC).Add(-ago)),
		}
	}
	cs := connect(t, mcpserver.Options{},
		ev("a", "Pod", "web-1", corev1.EventTypeNormal, "Pulled", "pulled image", 3*time.Minute),
		ev("b", "Pod", "web-1", corev1.EventTypeWarning, "BackOff", "Back-off restarting; password=s3cret!", time.Minute),
		ev("c", "ReplicaSet", "web-7d9f", corev1.EventTypeWarning, "FailedCreate", "exceeded quota", 2*time.Minute),
	)

	var all mcpserver.EventsOutput
	call(t, cs, "get_events", nil, &all)
	if len(all.Events) != 3 || all.Events[0].Reason != "BackOff" || all.Events[2].Reason != "Pulled" {
		t.Fatalf("want 3 events newest first, got %+v", all.Events)
	}
	if strings.Contains(all.Events[0].Message, "s3cret") {
		t.Errorf("event message not redacted: %q", all.Events[0].Message)
	}
	if all.Events[0].Object != "Pod/web-1" || all.Events[0].LastSeen != "2026-01-01T11:59:00Z" || all.Note == "" {
		t.Errorf("unexpected event: %+v", all.Events[0])
	}

	var filtered mcpserver.EventsOutput
	call(t, cs, "get_events", map[string]any{"kind": "Pod", "name": "web-1", "warnings_only": true}, &filtered)
	if len(filtered.Events) != 1 || filtered.Events[0].Reason != "BackOff" {
		t.Errorf("filtered events = %+v, want only the BackOff warning", filtered.Events)
	}
}
