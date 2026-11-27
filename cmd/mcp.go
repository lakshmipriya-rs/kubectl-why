// SPDX-License-Identifier: MIT

package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/lakshmipriya-rs/kubectl-why/pkg/mcpserver"
)

var allowedNamespaces []string

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Run an MCP server so AI assistants (Claude Desktop, Claude Code, …) can use kubectl-why",
	Long: `Runs a Model Context Protocol server over stdin/stdout. An MCP client such as
Claude Desktop or Claude Code starts it and calls its read-only tools:

  diagnose        diagnose a pod, deploy/<name>, svc/<name>, … (same rules as the CLI)
  list_unhealthy  list failing pods in a namespace
  get_events      list recent events, optionally for one object

It uses your kubeconfig and current context, exactly like kubectl. It only
reads from the cluster, and everything it returns is redacted.

Note: whatever the tools return is sent to the AI service your client uses.
Use --allowed-namespaces to limit what can be read.`,
	Example: `  # Claude Code
  claude mcp add kubectl-why -- kubectl-why mcp

  # Limit the server to two namespaces
  kubectl-why mcp --allowed-namespaces shop,billing`,
	Args: cobra.NoArgs,
	RunE: runMCP,
}

func init() {
	mcpCmd.Flags().StringSliceVar(&allowedNamespaces, "allowed-namespaces", nil,
		"Comma-separated namespaces the tools may read (default: any namespace your kubeconfig can read)")
	rootCmd.AddCommand(mcpCmd)
}

func runMCP(cmd *cobra.Command, _ []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client, namespace, err := newClient(f.kubeconfig, f.namespace)
	if err != nil {
		return err
	}
	// Never default to a namespace the server isn't allowed to read.
	if len(allowedNamespaces) > 0 && !slices.Contains(allowedNamespaces, namespace) {
		if f.namespace != "" {
			return fmt.Errorf("--namespace %q is not in --allowed-namespaces %v", f.namespace, allowedNamespaces)
		}
		namespace = allowedNamespaces[0]
	}
	// stdout carries the MCP protocol; anything human-readable goes to stderr.
	fmt.Fprintf(os.Stderr, "kubectl-why MCP server %s: default namespace %q", rootCmd.Version, namespace)
	if len(allowedNamespaces) > 0 {
		fmt.Fprintf(os.Stderr, ", allowed namespaces %v", allowedNamespaces)
	}
	fmt.Fprintln(os.Stderr)

	srv := mcpserver.New(mcpserver.Options{
		Client:            client,
		DefaultNamespace:  namespace,
		AllowedNamespaces: allowedNamespaces,
		Version:           rootCmd.Version,
	})
	if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}
