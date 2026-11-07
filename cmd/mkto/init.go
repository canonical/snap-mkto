// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"

	"github.com/canonical/snap-mkto/internal/config"
	"github.com/spf13/cobra"
)

func newInitCmd(env Env) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write a commented config template (dir 0700, file 0600)",
		Args:  func(c *cobra.Command, a []string) error { return usageErr(cobra.NoArgs(c, a)) },
		RunE: func(c *cobra.Command, _ []string) error {
			flag, _ := c.Flags().GetString("config")
			path, err := config.Path(flag, env.Getenv)
			if err != nil {
				return err
			}
			if err := config.WriteTemplate(path, force); err != nil {
				return err
			}
			fmt.Fprintf(env.Stdout, `Wrote %s

Next steps:
  1. Edit the file and set base_url, client_id and client_secret
     (Marketo Admin > Web Services; credentials under Admin > LaunchPoint).
  2. Set daily_quota to your subscription's daily API limit.
  3. Run: mkto quota
`, path)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing config file")
	return cmd
}
