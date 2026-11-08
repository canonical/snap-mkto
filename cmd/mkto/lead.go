// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/canonical/snap-mkto/internal/config"
	"github.com/canonical/snap-mkto/internal/marketo"
	"github.com/canonical/snap-mkto/internal/output"
)

func newLeadCmd(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lead <email|value>",
		Short: "Show a lead's profile",
		Long: "Show a lead's profile.\n\n" +
			"--lists adds the static lists the lead is on, newest first, by id: names aren't available\n" +
			"through this endpoint; find the list by id in Marketo. Smart lists can't be read through the API.",
		Args: func(c *cobra.Command, a []string) error { return usageErr(cobra.ExactArgs(1)(c, a)) },
		RunE: func(cmd *cobra.Command, args []string) error {
			value := strings.TrimSpace(args[0])
			by, _ := cmd.Flags().GetString("by")
			if err := checkKey(by, value); err != nil {
				return err
			}
			var fields []string
			if cmd.Flags().Changed("fields") {
				flag, _ := cmd.Flags().GetString("fields")
				for _, f := range strings.Split(flag, ",") {
					if f = strings.TrimSpace(f); f != "" {
						fields = append(fields, f)
					}
				}
				if len(fields) == 0 {
					return exitError{2, errors.New("--fields needs at least one field name, e.g. --fields email,leadScore")}
				}
			}
			cfg, cfgPath, client, err := newClient(cmd, env)
			if err != nil {
				return err
			}
			// updatedAt is for the side-by-side view of duplicates.
			extras := append(slices.Clone(cfg.Marketo.ProfileFields), "updatedAt")
			if fields != nil {
				// The user's own names for this run: a missing one is fatal, like lead_fields.
				cfg.Marketo.LeadFields, cfg.Marketo.ProfileFields, extras = fields, nil, []string{"id", "updatedAt"}
			}
			leads, err := leadsBy(cmd.Context(), cmd, env, client, cfg, by, value, extras)
			if fe, ok := errors.AsType[*marketo.FieldError](err); ok && cmd.Flags().Changed("fields") && inLeadFields(cfg, fe.Field) {
				return fmt.Errorf("field %q not found on this Marketo instance: check --fields", fe.Field)
			}
			if err != nil {
				return mapErr(err, cfg, cfgPath)
			}
			if leadID, _ := cmd.Flags().GetInt("lead-id"); len(leads) == 0 || leadID != 0 {
				l, _, err := pickLead(leads, leadID, by, value)
				if err != nil {
					return err
				}
				leads = []marketo.Lead{l}
			}
			withLists, _ := cmd.Flags().GetBool("lists")
			var lists []marketo.ListMembership
			if withLists {
				if len(leads) > 1 {
					return exitError{2, fmt.Errorf("--lists needs one lead: use --lead-id (ids: %s)", strings.Join(leadIDs(leads), ", "))}
				}
				if lists, err = client.ListMemberships(cmd.Context(), leads[0].ID()); err != nil {
					return mapErr(err, cfg, cfgPath)
				}
				slices.SortStableFunc(lists, func(a, b marketo.ListMembership) int { return b.Added.Compare(a.Added) }) // undated last
			}
			if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
				out := make([]output.LeadJSON, 0, len(leads))
				for _, l := range leads {
					f := map[string]any{}
					for _, k := range cfg.Marketo.LeadFields {
						f[k] = l[k]
					}
					for _, k := range cfg.Marketo.ProfileFields {
						if l[k] != nil && !inLeadFields(cfg, k) {
							f[k] = l[k]
						}
					}
					out = append(out, output.LeadJSON{ID: l.ID(), Fields: f})
				}
				if withLists {
					js := make([]output.ListJSON, 0, len(lists))
					for _, m := range lists {
						js = append(js, output.ListJSON{ID: m.ListID, Added: m.Added})
					}
					out[0].Lists = &js
				}
				return output.WriteJSON(env.Stdout, out)
			}
			if err := writeLeads(env, cfg, leads, by); err != nil || !withLists {
				return err
			}
			return writeLists(env, cfg, lists)
		},
	}
	cmd.Flags().String("fields", "", "comma-separated lead fields to show this run (replaces lead_fields and profile_fields)")
	cmd.Flags().String("by", "email", byHelp)
	cmd.Flags().Int("lead-id", 0, "show only this lead when several match")
	cmd.Flags().Bool("lists", false, "also show the static lists the lead is on (ids only; smart lists can't be read)")
	return cmd
}

func writeLeads(env Env, cfg *config.Config, leads []marketo.Lead, by string) error {
	loc, err := cfg.Location()
	if err != nil {
		return err
	}
	if len(leads) > 1 {
		ids := leadIDs(leads)
		// ahead of stdout, so it can't scroll away under the table
		fmt.Fprintf(env.Stderr, "note: %d leads share this %s (ids: %s); use --lead-id to pick one\n",
			len(leads), output.Clean(by), strings.Join(ids, ", "))
		return writeSideBySide(env, cfg, leads, ids, loc)
	}
	for _, l := range leads {
		fmt.Fprintf(env.Stdout, "Lead %d\n", l.ID())
		rows := make([][]string, 0, len(cfg.Marketo.LeadFields))
		for _, k := range cfg.Marketo.LeadFields {
			rows = append(rows, []string{k, display(l[k], loc)})
		}
		for _, k := range cfg.Marketo.ProfileFields {
			if v := display(l[k], loc); v != "" && !inLeadFields(cfg, k) {
				rows = append(rows, []string{k, v})
			}
		}
		if err := output.Table(env.Stdout, rows); err != nil {
			return err
		}
	}
	return nil
}

func leadIDs(leads []marketo.Lead) []string {
	ids := make([]string, len(leads))
	for i, l := range leads {
		ids[i] = strconv.Itoa(l.ID())
	}
	return ids
}

// writeLists prints the static lists after the profile, in the order given.
func writeLists(env Env, cfg *config.Config, lists []marketo.ListMembership) error {
	if len(lists) == 0 {
		fmt.Fprint(env.Stdout, "\nStatic lists: none\n")
		return nil
	}
	loc, err := cfg.Location()
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "\nStatic lists (%d)\n", len(lists))
	rows := [][]string{{"LIST ID", "ADDED"}}
	for _, m := range lists {
		added := "-"
		if !m.Added.IsZero() {
			added = m.Added.In(loc).Format("2006-01-02 15:04")
		}
		rows = append(rows, []string{strconv.Itoa(m.ListID), added})
	}
	return output.Table(env.Stdout, rows)
}

// writeSideBySide shows duplicates one column per lead: id and updatedAt, then only the
// fields whose values differ. Profile fields empty in every lead are left out, as in the
// one-lead view.
func writeSideBySide(env Env, cfg *config.Config, leads []marketo.Lead, ids []string, loc *time.Location) error {
	updated := []string{"updatedAt"}
	for _, l := range leads {
		updated = append(updated, display(l["updatedAt"], loc))
	}
	rows := [][]string{append([]string{"FIELD"}, ids...), append([]string{"id"}, ids...), updated}
	names := slices.Clone(cfg.Marketo.LeadFields)
	for _, k := range cfg.Marketo.ProfileFields {
		if !inLeadFields(cfg, k) {
			names = append(names, k)
		}
	}
	same := 0
	for _, k := range names {
		if strings.EqualFold(k, "id") || strings.EqualFold(k, "updatedAt") {
			continue
		}
		row, differ, shown := []string{k}, false, inLeadFields(cfg, k)
		for _, l := range leads {
			v := display(l[k], loc)
			row = append(row, v)
			differ, shown = differ || v != row[1], shown || v != ""
		}
		switch {
		case differ:
			rows = append(rows, row)
		case shown:
			same++
		}
	}
	if err := output.Table(env.Stdout, rows); err != nil {
		return err
	}
	if same == 1 {
		fmt.Fprintln(env.Stdout, "1 field is the same in every lead (not shown)")
	} else if same > 1 {
		fmt.Fprintf(env.Stdout, "%d fields are the same in every lead (not shown)\n", same)
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
