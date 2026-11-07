// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/canonical/snap-mkto/internal/config"
	"github.com/canonical/snap-mkto/internal/marketo"
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
		marketo.Options{HTTP: env.HTTP, Now: env.Now, Sleep: env.Sleep, Log: log})
	return cfg, path, c, nil
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
		if slices.ContainsFunc(cfg.Marketo.LeadFields, func(f string) bool { return strings.EqualFold(f, fe.Field) }) {
			return fmt.Errorf("field %q not found: fix lead_fields in %s", fe.Field, cfgPath)
		}
		return fmt.Errorf("field %q not found. It isn't in lead_fields, but mkto also requests id, updatedAt, title, company, industry, country and numberOfEmployees; this Marketo instance may not have that field", fe.Field)
	}
	return err
}
