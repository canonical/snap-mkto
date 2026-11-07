// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/canonical/snap-mkto/internal/config"
	"github.com/canonical/snap-mkto/internal/marketo"
	"github.com/canonical/snap-mkto/internal/output"
)

func newLeadCmd(env Env) *cobra.Command {
	return &cobra.Command{
		Use:   "lead <email>",
		Short: "Show a lead's profile",
		Args:  func(c *cobra.Command, a []string) error { return usageErr(cobra.ExactArgs(1)(c, a)) },
		RunE: func(cmd *cobra.Command, args []string) error {
			email := strings.TrimSpace(args[0])
			cfg, cfgPath, client, err := newClient(cmd, env)
			if err != nil {
				return err
			}
			leads, err := client.LeadsByEmail(cmd.Context(), email, cfg.Marketo.LeadFields)
			if err != nil {
				return mapErr(err, cfg, cfgPath)
			}
			if len(leads) == 0 {
				return exitError{3, fmt.Errorf("no lead found for %s", email)}
			}
			if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
				out := make([]output.LeadJSON, 0, len(leads))
				for _, l := range leads {
					f := map[string]any{}
					for _, k := range cfg.Marketo.LeadFields {
						f[k] = l[k]
					}
					out = append(out, output.LeadJSON{ID: l.ID(), Fields: f})
				}
				return output.WriteJSON(env.Stdout, out)
			}
			return writeLeads(env, cfg, leads)
		},
	}
}

func writeLeads(env Env, cfg *config.Config, leads []marketo.Lead) error {
	loc, err := cfg.Location()
	if err != nil {
		return err
	}
	ids := make([]string, len(leads))
	for i, l := range leads {
		ids[i] = strconv.Itoa(l.ID())
		if i > 0 {
			fmt.Fprintln(env.Stdout)
		}
		fmt.Fprintf(env.Stdout, "Lead %d\n", l.ID())
		rows := make([][]string, 0, len(cfg.Marketo.LeadFields))
		for _, k := range cfg.Marketo.LeadFields {
			rows = append(rows, []string{k, display(l[k], loc)})
		}
		if err := output.Table(env.Stdout, rows); err != nil {
			return err
		}
	}
	if len(leads) > 1 {
		fmt.Fprintf(env.Stdout, "note: %d leads share this email (ids: %s); use --lead-id with mkto activity\n",
			len(leads), strings.Join(ids, ", "))
	}
	return nil
}

// display renders a field value; RFC 3339 timestamps move to the display zone.
func display(v any, loc *time.Location) string {
	if v == nil {
		return ""
	}
	s := fmt.Sprint(v)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.In(loc).Format("2006-01-02 15:04")
	}
	return s
}
