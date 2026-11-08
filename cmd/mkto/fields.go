// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/canonical/snap-mkto/internal/marketo"
	"github.com/canonical/snap-mkto/internal/output"
)

func newFieldsCmd(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fields",
		Short: "List lead fields (REST names for lead_fields, profile_fields and --fields)",
		Args:  func(c *cobra.Command, a []string) error { return usageErr(cobra.NoArgs(c, a)) },
		RunE:  func(cmd *cobra.Command, _ []string) error { return runFields(cmd, env) },
	}
	cmd.Flags().String("search", "", "show only fields whose REST name or label contains this text (case-insensitive)")
	return cmd
}

func runFields(cmd *cobra.Command, env Env) error {
	search, _ := cmd.Flags().GetString("search")
	asJSON, _ := cmd.Flags().GetBool("json")
	cfg, cfgPath, client, err := newClient(cmd, env)
	if err != nil {
		return err
	}
	all, err := client.DescribeLeads(cmd.Context())
	if err != nil {
		return mapErr(err, cfg, cfgPath)
	}
	slices.SortFunc(all, func(a, b marketo.Field) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	s := strings.ToLower(search)
	skipped := 0
	var rows [][]string
	list := []output.FieldJSON{}
	for _, f := range all {
		if f.Name == "" {
			skipped++
			continue
		}
		if !strings.Contains(strings.ToLower(f.Name), s) && !strings.Contains(strings.ToLower(f.Label), s) {
			continue
		}
		list = append(list, output.FieldJSON{Name: f.Name, Label: f.Label, Type: f.Type, Length: f.Length, ReadOnly: f.ReadOnly})
		length, ro := "-", "-"
		if f.Length > 0 {
			length = fmt.Sprint(f.Length)
		}
		if f.ReadOnly {
			ro = "yes"
		}
		rows = append(rows, []string{f.Name, f.Label, f.Type, length, ro})
	}
	var buf bytes.Buffer
	switch {
	case asJSON:
		err = output.WriteJSON(&buf, list)
	case len(rows) == 0:
		fmt.Fprintf(&buf, "No lead fields match %q.\n", search)
	default:
		err = output.Table(&buf, append([][]string{{"NAME", "LABEL", "TYPE", "LENGTH", "READ-ONLY"}}, rows...))
	}
	if err != nil {
		return err
	}
	switch { // notes go to stderr, so stdout stays the table alone
	case asJSON:
	case skipped == 1:
		fmt.Fprintln(env.Stderr, "note: 1 field has no REST name and is not shown")
	case skipped > 1:
		fmt.Fprintf(env.Stderr, "note: %d fields have no REST name and are not shown\n", skipped)
	}
	_, err = env.Stdout.Write(buf.Bytes())
	return err
}
