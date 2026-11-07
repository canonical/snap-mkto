// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/canonical/snap-mkto/internal/output"
)

func newRootCmd(env Env) *cobra.Command {
	root := &cobra.Command{
		Use:           "mkto",
		Short:         "Read-only Marketo Engage client",
		Args:          func(c *cobra.Command, a []string) error { return usageErr(cobra.NoArgs(c, a)) },
		RunE:          func(c *cobra.Command, _ []string) error { return c.Help() },
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.SetOut(env.Stdout)
	root.SetErr(env.Stderr)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageErr(err) })
	root.PersistentFlags().String("config", "", "config file path (default: see docs/SPEC.md)")
	root.PersistentFlags().Bool("json", false, "output JSON")
	root.PersistentFlags().Bool("verbose", false, "log request method, path, status and timing to stderr")
	root.AddCommand(newVersionCmd(env), newBriefCmd(env), newInitCmd(env), newLeadCmd(env), newActivityCmd(env), newProgramsCmd(env), newQuotaCmd(env))
	return root
}

func run(args []string, env Env) int { return runContext(context.Background(), args, env) }

func runContext(ctx context.Context, args []string, env Env) int {
	root := newRootCmd(env)
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	if err != nil {
		fmt.Fprintln(env.Stderr, output.Clean(err.Error()))
	}
	return exitCode(err)
}
