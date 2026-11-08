// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/canonical/snap-mkto/internal/config"
	"github.com/canonical/snap-mkto/internal/output"
)

func newRootCmd(env Env) *cobra.Command {
	root := &cobra.Command{
		Use:   "mkto",
		Short: "Read-only Marketo Engage client",
		Args:  unknownCommand,
		// cobra's default distance, which SuggestionsFor alone doesn't apply
		SuggestionsMinimumDistance: 2,
		RunE:                       func(c *cobra.Command, _ []string) error { return c.Help() },
		SilenceErrors:              true,
		SilenceUsage:               true,
	}
	root.SetOut(env.Stdout)
	root.SetErr(env.Stderr)
	root.SetFlagErrorFunc(flagErr)
	help := "config file path (default: see docs/SPEC.md)"
	if p, err := config.Path("", env.Getenv); err == nil {
		help = "config file path (default: " + p + ")"
	}
	root.PersistentFlags().String("config", "", help)
	root.PersistentFlags().Bool("json", false, "output JSON")
	root.PersistentFlags().Bool("verbose", false, "log request method, path, status and timing to stderr")
	root.AddCommand(newVersionCmd(env), newBriefCmd(env), newInitCmd(env), newLeadCmd(env), newLeadsCmd(env), newAccountCmd(env), newActivityCmd(env), newWhyCmd(env), newProgramsCmd(env), newTypesCmd(env), newFieldsCmd(env), newDoctorCmd(env), newQuotaCmd(env))
	return root
}

// unknownCommand is cobra's unknown-command error with its "Did you mean" block, split
// into joined lines so the printer keeps the layout (Clean would mangle \n and \t).
func unknownCommand(c *cobra.Command, a []string) error {
	if len(a) == 0 {
		return nil
	}
	parts := []error{usageErr(fmt.Errorf("unknown command %q for %q", a[0], c.CommandPath()))}
	if s := c.SuggestionsFor(a[0]); len(s) > 0 {
		parts = append(parts, errors.New(""), errors.New("Did you mean this?"))
		for _, n := range s {
			parts = append(parts, errors.New("  "+n))
		}
	}
	return errors.Join(parts...)
}

// flagErr marks flag errors as usage errors, and rewords a bad --days or --lead-id number.
func flagErr(_ *cobra.Command, err error) error {
	if _, ok := errors.AsType[*strconv.NumError](err); ok {
		for _, f := range [][2]string{
			{"days", "--days takes a whole number of days, e.g. --days 30"},
			{"lead-id", "--lead-id takes a lead id number, e.g. --lead-id 1001"},
		} {
			if strings.Contains(err.Error(), `for "--`+f[0]+`" flag:`) {
				return usageErr(errors.New(f[1]))
			}
		}
	}
	return usageErr(err)
}

func run(args []string, env Env) int { return runContext(context.Background(), args, env) }

func runContext(ctx context.Context, args []string, env Env) int {
	root := newRootCmd(env)
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	// A joined error (several config problems) prints one cleaned line per part; Clean
	// would otherwise turn its newlines into U+FFFD.
	parts := []error{err}
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		parts = j.Unwrap()
	}
	for _, e := range parts {
		if e != nil {
			fmt.Fprintln(env.Stderr, output.Clean(e.Error()))
		}
	}
	return exitCode(err)
}
