// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/canonical/snap-mkto/internal/config"
	"github.com/canonical/snap-mkto/internal/marketo"
	"github.com/canonical/snap-mkto/internal/output"
)

func newDoctorCmd(env Env) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check the config, credentials, API permissions and configured names",
		Args:  func(c *cobra.Command, a []string) error { return usageErr(cobra.NoArgs(c, a)) },
		RunE:  func(cmd *cobra.Command, _ []string) error { return runDoctor(cmd, env) },
	}
}

func runDoctor(cmd *cobra.Command, env Env) error {
	ctx := cmd.Context()
	res := output.DoctorJSON{Checks: []output.CheckJSON{}}
	add := func(name, status, detail string) {
		res.Checks = append(res.Checks, output.CheckJSON{Name: name, Status: status, Detail: detail})
	}
	cfg, path, client, err := newClient(cmd, env)
	if err != nil {
		add("config", "fail", err.Error())
		return writeDoctor(cmd, env, res)
	}
	add("config", "ok", path)
	if fi, err := os.Stat(path); err == nil && fi.Mode().Perm()&0o077 != 0 {
		add("config permissions", "warn", fmt.Sprintf("%s is readable by other users and holds credentials; run: chmod 600 %s", path, path))
	}
	// Quota and Ctrl-C end the run at once; any other failure is reported and the checks go on.
	stop := func(err error) bool {
		return err != nil && (errors.Is(err, marketo.ErrQuotaExhausted) || ctx.Err() != nil)
	}

	types, typesErr := client.ActivityTypes(ctx) // the first call, so it fetches the token
	if stop(typesErr) {
		return mapErr(typesErr, cfg, path)
	}
	if !client.HasToken() {
		add("credentials", "fail", mapErr(typesErr, cfg, path).Error())
		return writeDoctor(cmd, env, res)
	}
	add("credentials", "ok", "access token fetched")
	if typesErr != nil {
		add("activity types", "fail", mapErr(typesErr, cfg, path).Error())
	} else {
		add("activity types", "ok", fmt.Sprintf("%d types", len(types)))
	}

	fields, fieldsErr := client.DescribeLeads(ctx)
	if stop(fieldsErr) {
		return mapErr(fieldsErr, cfg, path)
	}
	if fieldsErr != nil {
		add("lead fields", "fail", mapErr(fieldsErr, cfg, path).Error())
	} else {
		add("lead fields", "ok", fmt.Sprintf("%d fields", len(fields)))
	}

	err = client.CheckAssets(ctx)
	if stop(err) {
		return mapErr(err, cfg, path)
	}
	ae, isAPI := errors.AsType[*marketo.APIError](err)
	he, isHTTP := errors.AsType[*marketo.HTTPError](err)
	switch {
	case err == nil:
		add("assets", "ok", "programs readable")
	case isAPI && ae.Code == "603" || isHTTP && (he.Status == 401 || he.Status == 403):
		add("assets", "fail", "access denied. Ask Marketing Ops to give the API role Read-Only Assets (needed for program names, program tokens and smart campaign names)")
	default:
		add("assets", "fail", mapErr(err, cfg, path).Error())
	}

	if typesErr == nil && fieldsErr == nil {
		n := len(res.Checks)
		checkNames(cfg, path, types, fields, add)
		if len(res.Checks) == n {
			add("names", "ok", "every configured field and activity type is on this instance")
		}
	}
	return writeDoctor(cmd, env, res)
}

// checkNames adds a check for each config list naming fields or types this instance lacks.
func checkNames(cfg *config.Config, path string, types []marketo.ActivityType, fields []marketo.Field, add func(name, status, detail string)) {
	// Adobe documents these as returned for every lead, whether describe lists them or not.
	names := map[string]bool{"id": true, "email": true, "firstname": true, "lastname": true, "createdat": true, "updatedat": true}
	labels := map[string]bool{}
	for _, f := range fields {
		names[strings.ToLower(f.Name)] = true
		labels[strings.ToLower(strings.TrimSpace(f.Label))] = true
	}
	missing := func(list []string, known map[string]bool) string {
		var out []string
		for _, n := range list {
			if !known[strings.ToLower(strings.TrimSpace(n))] {
				out = append(out, n)
			}
		}
		return strings.Join(out, ", ")
	}
	m := &cfg.Marketo
	if s := missing(m.LeadFields, names); s != "" {
		add("lead_fields", "fail", fmt.Sprintf("not on this instance: %s. `mkto lead` fails until you fix lead_fields in %s (`mkto fields` lists the names)", s, path))
	}
	if s := missing(m.ProfileFields, names); s != "" {
		add("profile_fields", "warn", fmt.Sprintf("not on this instance: %s. Each missing profile field costs one extra API call per lead lookup; remove it from profile_fields in %s", s, path))
	}
	// A Change Data Value row names its field by display name, so change_fields are labels.
	if s := missing(m.ChangeFields, labels); s != "" {
		add("change_fields", "warn", fmt.Sprintf("no field has this label on this instance: %s. Fix change_fields in %s (`mkto fields` lists the labels)", s, path))
	}

	ids, unknown := marketo.ResolveTypes(types, m.ActivityTypes)
	var parts []string
	if m.ActivityTypesMigrated {
		parts = append(parts, fmt.Sprintf("activity_types is the unedited v0.1.0 list, so mkto uses the 0.2.0 defaults; delete activity_types from %s to drop this warning", path))
	}
	if len(unknown) > 0 {
		if m.ActivityTypesDefaulted {
			parts = append(parts, fmt.Sprintf("default activity types not on this instance: %s. mkto skips them; to choose your own, set activity_types in %s (`mkto types` lists the names)", strings.Join(unknown, ", "), path))
		} else {
			for i, n := range unknown {
				if s := closestType(types, n); s != "" {
					unknown[i] = fmt.Sprintf("%s (did you mean %q?)", n, s)
				}
			}
			lead := "not on this instance"
			if len(ids) == 0 {
				lead = "none of activity_types is on this instance"
			}
			parts = append(parts, fmt.Sprintf("%s: %s. Fix activity_types in %s (`mkto types` lists the names)", lead, strings.Join(unknown, ", "), path))
		}
	}
	switch {
	case len(ids) == 0: // activity has nothing to fetch
		add("activity_types", "fail", strings.Join(parts, " "))
	case len(parts) > 0:
		add("activity_types", "warn", strings.Join(parts, " "))
	}
	// Like change_fields, [why]'s fields are labels; checked only when [why] names any.
	var whyFields []string
	for _, f := range []string{cfg.Why.ScoreField, cfg.Why.StatusField} {
		if strings.TrimSpace(f) != "" {
			whyFields = append(whyFields, f)
		}
	}
	if s := missing(whyFields, labels); s != "" {
		add("why", "warn", fmt.Sprintf("no field has this label on this instance: %s. score_field and status_field are labels, "+
			"as in the LABEL column of `mkto fields`; fix them in [why] in %s", s, path))
	}
}

// writeDoctor prints one line per check (✓ ok, ! warning, ✗ failure), or JSON, and fails
// when any check did.
func writeDoctor(cmd *cobra.Command, env Env, res output.DoctorJSON) error {
	failed := 0
	var buf bytes.Buffer
	for _, c := range res.Checks {
		if c.Status == "fail" {
			failed++
		}
		// A config error can hold several lines; later ones are indented under the first.
		lines := strings.Split(c.Detail, "\n")
		fmt.Fprintf(&buf, "%s %s: %s\n", map[string]string{"ok": "✓", "warn": "!", "fail": "✗"}[c.Status], c.Name, output.Clean(lines[0]))
		for _, l := range lines[1:] {
			fmt.Fprintf(&buf, "  %s\n", output.Clean(l))
		}
	}
	if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
		buf.Reset()
		if err := output.WriteJSON(&buf, res); err != nil {
			return err
		}
	}
	if _, err := env.Stdout.Write(buf.Bytes()); err != nil {
		return err
	}
	switch failed {
	case 0:
		return nil
	case 1:
		return errors.New("1 check failed")
	}
	return fmt.Errorf("%d checks failed", failed)
}
