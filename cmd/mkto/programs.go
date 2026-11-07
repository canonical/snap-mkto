// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/canonical/snap-mkto/internal/output"
)

func newProgramsCmd(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "programs",
		Short: "List Marketo programs",
		Args:  func(c *cobra.Command, a []string) error { return usageErr(cobra.NoArgs(c, a)) },
		RunE:  func(cmd *cobra.Command, _ []string) error { return runPrograms(cmd, env) },
	}
	cmd.Flags().String("name", "", "show only programs whose name contains this text (case-insensitive)")
	return cmd
}

func runPrograms(cmd *cobra.Command, env Env) error {
	name, _ := cmd.Flags().GetString("name")
	asJSON, _ := cmd.Flags().GetBool("json")
	cfg, cfgPath, client, err := newClient(cmd, env)
	if err != nil {
		return err
	}
	loc, err := cfg.Location()
	if err != nil {
		return err
	}
	all, err := client.Programs(cmd.Context())
	if err != nil {
		return mapErr(err, cfg, cfgPath)
	}
	var rows [][]string
	list := []output.ProgramJSON{}
	for _, p := range all {
		if !strings.Contains(strings.ToLower(p.Name), strings.ToLower(name)) {
			continue
		}
		created := ""
		if !p.CreatedAt.IsZero() {
			created = p.CreatedAt.In(loc).Format("2006-01-02")
		}
		list = append(list, output.ProgramJSON{ID: p.ID, Name: p.Name, Type: p.Type, Channel: p.Channel, Workspace: p.Workspace, CreatedAt: p.CreatedAt.UTC()})
		rows = append(rows, []string{fmt.Sprint(p.ID), p.Name, p.Type, p.Channel, p.Workspace, created})
	}
	var buf bytes.Buffer
	switch {
	case asJSON:
		err = output.WriteJSON(&buf, list)
	case len(rows) == 0:
		fmt.Fprintf(&buf, "No programs match %q.\n", name)
	default:
		err = output.Table(&buf, append([][]string{{"ID", "NAME", "TYPE", "CHANNEL", "WORKSPACE", "CREATED"}}, rows...))
	}
	if err != nil {
		return err
	}
	_, err = env.Stdout.Write(buf.Bytes())
	return err
}
