// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"fmt"
	"math"

	"github.com/spf13/cobra"

	"github.com/canonical/snap-mkto/internal/output"
)

func newQuotaCmd(env Env) *cobra.Command {
	return &cobra.Command{
		Use:   "quota",
		Short: "Show today's API usage against daily_quota",
		Args:  func(c *cobra.Command, a []string) error { return usageErr(cobra.NoArgs(c, a)) },
		RunE:  func(cmd *cobra.Command, _ []string) error { return runQuota(cmd, env) },
	}
}

func runQuota(cmd *cobra.Command, env Env) error {
	asJSON, _ := cmd.Flags().GetBool("json")
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
		err = output.WriteJSON(&buf, res)
	} else {
		fmt.Fprintf(&buf, "Today (%s, US Central): %d of %d calls used (%.1f%%), %d remaining\n", today.Date, today.Total, limit, percent, remaining)
		fmt.Fprintf(&buf, "The daily limit comes from daily_quota in %s; Marketo doesn't report it.\n\nLast 7 days\n", cfgPath)
		rows := [][]string{{"DATE", "CALLS"}}
		for _, d := range week {
			rows = append(rows, []string{d.Date, fmt.Sprint(d.Total)})
		}
		err = output.Table(&buf, rows)
	}
	if err != nil {
		return err
	}
	_, err = env.Stdout.Write(buf.Bytes())
	return err
}
