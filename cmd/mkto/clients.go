// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/canonical/snap-mkto/internal/config"
	"github.com/canonical/snap-mkto/internal/marketo"
	"github.com/canonical/snap-mkto/internal/output"
)

// newClient loads the config and builds the Marketo client; shared by all data commands.
// It also returns the config path it resolved, for messages that name the file.
func newClient(cmd *cobra.Command, env Env) (*config.Config, string, *marketo.Client, error) {
	flag, _ := cmd.Flags().GetString("config")
	path, err := config.Path(flag, env.Getenv)
	if err != nil {
		return nil, "", nil, err
	}
	cfg, err := config.Load(path, env.Getenv, env.Stderr)
	if err != nil {
		return nil, "", nil, err
	}
	var log io.Writer
	if v, _ := cmd.Flags().GetBool("verbose"); v {
		log = env.Stderr
	}
	c := marketo.New(cfg.Marketo.BaseURL, cfg.Marketo.ClientID, cfg.Marketo.ClientSecret,
		marketo.Options{HTTP: env.HTTP, Now: env.Now, Sleep: env.Sleep, Log: log, Progress: env.Stderr})
	return cfg, path, c, nil
}

// leadsByEmail looks a lead up with lead_fields plus extras, the fields mkto adds itself.
// A 1006 for an extra drops that field and retries, so at most len(extras) retries;
// a 1006 for one of the user's lead_fields is returned for mapErr.
func leadsByEmail(ctx context.Context, cmd *cobra.Command, env Env, client *marketo.Client, cfg *config.Config, email string, extras []string) ([]marketo.Lead, error) {
	fields := slices.Clone(cfg.Marketo.LeadFields)
	for _, k := range extras {
		if !slices.Contains(fields, k) {
			fields = append(fields, k)
		}
	}
	verbose, _ := cmd.Flags().GetBool("verbose")
	for {
		leads, err := client.LeadsByEmail(ctx, email, fields)
		fe, ok := errors.AsType[*marketo.FieldError](err)
		// id is the lookup key and the names feed brief's redaction guard: never drop them.
		if !ok || inLeadFields(cfg, fe.Field) || slices.Contains([]string{"id", "email", "firstName", "lastName"}, fe.Field) {
			return leads, err
		}
		i := slices.IndexFunc(fields, func(f string) bool { return strings.EqualFold(f, fe.Field) })
		if i < 0 {
			return leads, err
		}
		fields = slices.Delete(fields, i, i+1)
		if verbose {
			fmt.Fprintf(env.Stderr, "note: field %q isn't on this Marketo instance; skipped\n", output.Clean(fe.Field))
		}
	}
}

func inLeadFields(cfg *config.Config, field string) bool {
	return slices.ContainsFunc(cfg.Marketo.LeadFields, func(f string) bool { return strings.EqualFold(f, field) })
}

// mapErr turns client errors into exit codes; every data command uses it.
func mapErr(err error, cfg *config.Config, cfgPath string) error {
	var fe *marketo.FieldError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, marketo.ErrQuotaExhausted):
		return exitError{4, errors.New("daily API quota reached; it resets at 12:00 AM US Central. Run `mkto quota`.")}
	case errors.As(err, &fe):
		if inLeadFields(cfg, fe.Field) {
			return fmt.Errorf("field %q not found: fix lead_fields in %s", fe.Field, cfgPath)
		}
		return fmt.Errorf("field %q not found. It isn't in lead_fields, but mkto needs id, email, firstName and lastName (the lookup key and the redaction guard); this Marketo instance may not have that field", fe.Field)
	}
	return err
}
