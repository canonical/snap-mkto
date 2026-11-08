// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"cmp"
	"fmt"
	"math"
	"slices"

	"github.com/spf13/cobra"

	"github.com/canonical/snap-mkto/internal/marketo"
	"github.com/canonical/snap-mkto/internal/output"
)

// errorMeanings are the codes docs/SPEC.md's error table explains.
var errorMeanings = map[string]string{
	"601": "token invalid", "602": "token expired", "606": "rate limit", "607": "daily quota reached",
	"615": "concurrency limit", "713": "transient error", "1006": "field not found",
}

func newQuotaCmd(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "quota",
		Short: "Show today's API usage against daily_quota",
		Args:  func(c *cobra.Command, a []string) error { return usageErr(cobra.NoArgs(c, a)) },
		RunE:  func(cmd *cobra.Command, _ []string) error { return runQuota(cmd, env) },
	}
	cmd.Flags().Bool("by-user", false, "split today's calls by API user (no extra API call)")
	cmd.Flags().Bool("errors", false, "show today's API error codes (one extra API call)")
	return cmd
}

func runQuota(cmd *cobra.Command, env Env) error {
	asJSON, _ := cmd.Flags().GetBool("json")
	byUser, _ := cmd.Flags().GetBool("by-user")
	withErrors, _ := cmd.Flags().GetBool("errors")
	cfg, cfgPath, client, err := newClient(cmd, env)
	if err != nil {
		return err
	}
	today, err := client.UsageToday(cmd.Context())
	if err != nil {
		return mapErr(err, cfg, cfgPath)
	}
	week, err := client.UsageLast7Days(cmd.Context())
	if err != nil {
		return mapErr(err, cfg, cfgPath)
	}
	var errs []marketo.ErrorCount
	if withErrors {
		if errs, err = client.ErrorsToday(cmd.Context()); err != nil {
			return mapErr(err, cfg, cfgPath)
		}
	}
	slices.SortStableFunc(today.Users, func(a, b marketo.UserUsage) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), cmp.Compare(a.UserID, b.UserID))
	})
	slices.SortStableFunc(errs, func(a, b marketo.ErrorCount) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), cmp.Compare(a.Code, b.Code))
	})
	limit := cfg.Marketo.DailyQuota
	remaining := max(limit-today.Total, 0)
	percent := 0.0
	if limit > 0 {
		percent = math.Round(float64(today.Total)/float64(limit)*1000) / 10
	}
	if today.Total > limit {
		fmt.Fprintf(env.Stderr, "warning: usage is above daily_quota; check daily_quota in %s\n", cfgPath)
	}

	var buf bytes.Buffer
	if asJSON {
		res := output.QuotaJSON{Date: today.Date, Used: today.Total, Limit: limit, Remaining: remaining, Percent: percent, Last7Days: []output.UsageDayJSON{}}
		for _, d := range week {
			res.Last7Days = append(res.Last7Days, output.UsageDayJSON{Date: d.Date, Total: d.Total})
		}
		if byUser {
			users := []output.UserUsageJSON{}
			for _, u := range today.Users {
				users = append(users, output.UserUsageJSON{UserID: u.UserID, Count: u.Count})
			}
			res.ByUser = &users
		}
		if withErrors {
			codes := []output.ErrorCountJSON{}
			for _, e := range errs {
				codes = append(codes, output.ErrorCountJSON{Code: e.Code, Count: e.Count})
			}
			res.Errors = &codes
		}
		err = output.WriteJSON(&buf, res)
	} else {
		fmt.Fprintf(&buf, "Today (%s, US Central): %d of %d calls used (%.1f%%), %d remaining\n", today.Date, today.Total, limit, percent, remaining)
		fmt.Fprintf(&buf, "The daily limit comes from daily_quota in %s; Marketo doesn't report it.\n", cfgPath)
		if byUser {
			rows := [][]string{{"API USER", "CALLS"}}
			for _, u := range today.Users {
				rows = append(rows, []string{u.UserID, fmt.Sprint(u.Count)})
			}
			buf.WriteString("\n")
			_ = output.Table(&buf, rows) // writes to a bytes.Buffer
		}
		buf.WriteString("\nLast 7 days\n")
		rows := [][]string{{"DATE", "CALLS"}}
		for _, d := range week {
			rows = append(rows, []string{d.Date, fmt.Sprint(d.Total)})
		}
		err = output.Table(&buf, rows)
		if withErrors {
			buf.WriteString("\nAPI errors today\n")
			if len(errs) == 0 {
				buf.WriteString("No API errors today.\n")
			} else {
				rows := [][]string{{"ERROR", "COUNT"}}
				for _, e := range errs {
					code := e.Code
					if m, ok := errorMeanings[e.Code]; ok {
						code += " (" + m + ")"
					}
					rows = append(rows, []string{code, fmt.Sprint(e.Count)})
				}
				_ = output.Table(&buf, rows)
			}
		}
	}
	if err != nil {
		return err
	}
	_, err = env.Stdout.Write(buf.Bytes())
	return err
}
