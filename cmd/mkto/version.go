// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

var goVersion = runtime.Version() // a var so tests can pin it

type versionInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Go      string `json:"go"`
	Snap    string `json:"snap"` // "name (revision)", or empty outside a snap
}

func newVersionCmd(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Show version, commit, Go version and snap status",
		Args:  func(c *cobra.Command, a []string) error { return usageErr(cobra.NoArgs(c, a)) },
		RunE: func(c *cobra.Command, _ []string) error {
			asJSON, _ := c.Flags().GetBool("json")
			v := versionInfo{Version: version, Commit: commit, Go: goVersion}
			if name := env.Getenv("SNAP_NAME"); name != "" {
				v.Snap = fmt.Sprintf("%s (%s)", name, env.Getenv("SNAP_REVISION"))
			}
			if asJSON {
				b, err := json.MarshalIndent(v, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintf(env.Stdout, "%s\n", b)
				return nil
			}
			snap := "no"
			if v.Snap != "" {
				snap = v.Snap
			}
			fmt.Fprintf(env.Stdout, "mkto %s\ncommit: %s\ngo: %s\nsnap: %s\n", v.Version, v.Commit, v.Go, snap)
			return nil
		},
	}
	return cmd
}
