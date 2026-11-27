// SPDX-License-Identifier: MIT

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

const kubeconfigTemplate = `apiVersion: v1
kind: Config
clusters:
- name: c
  cluster: {server: "https://127.0.0.1:6443"}
users:
- name: u
  user: {token: "not-a-real-token"}
contexts:
- name: ctx
  context: {cluster: c, user: u%s}
current-context: ctx
`

func writeKubeconfig(t *testing.T, nsField string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(fmt.Sprintf(kubeconfigTemplate, nsField)), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Without -n, the context's namespace is used (as kubectl and `oc project` do).
func TestNewClient_Namespace(t *testing.T) {
	withNS := writeKubeconfig(t, ", namespace: shop")
	withoutNS := writeKubeconfig(t, "")

	tests := []struct {
		name, kubeconfig, flag, want string
	}{
		{"context namespace", withNS, "", "shop"},
		{"flag wins", withNS, "billing", "billing"},
		{"no namespace anywhere", withoutNS, "", "default"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, ns, err := newClient(tc.kubeconfig, tc.flag)
			if err != nil {
				t.Fatal(err)
			}
			if ns != tc.want {
				t.Errorf("namespace = %q, want %q", ns, tc.want)
			}
		})
	}
}
