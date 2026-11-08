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
	"unicode"

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

const byHelp = "look the lead up by this field instead of email, e.g. sfdcContactId or id"

// leadsBy looks leads up by field by (--by, "email" by default) with lead_fields plus
// extras, the fields mkto adds itself. A 1006 for an extra drops that field and retries,
// so at most len(extras) retries; a 1006 for one of the user's lead_fields is returned for mapErr.
func leadsBy(ctx context.Context, cmd *cobra.Command, env Env, client *marketo.Client, cfg *config.Config, by, value string, extras []string) ([]marketo.Lead, error) {
	return leadsUpTo(ctx, cmd, env, client, cfg, by, value, extras, 0)
}

// leadsUpTo is leadsBy with LeadsUpTo's page cap (0: no cap).
func leadsUpTo(ctx context.Context, cmd *cobra.Command, env Env, client *marketo.Client, cfg *config.Config, by, value string, extras []string, pageCap int) ([]marketo.Lead, error) {
	fields := slices.Clone(cfg.Marketo.LeadFields)
	for _, k := range extras {
		if !slices.Contains(fields, k) {
			fields = append(fields, k)
		}
	}
	verbose, _ := cmd.Flags().GetBool("verbose")
	for {
		leads, err := client.LeadsUpTo(ctx, by, []string{value}, fields, pageCap)
		// 1003's meaning is ambiguous in Marketo's docs, so keep its message and only add a hint.
		if ae, ok := errors.AsType[*marketo.APIError](err); ok && ae.Code == "1003" {
			return nil, fmt.Errorf("%w; check that %s is a searchable field (names: mkto fields)", err, by)
		}
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

// webTypes are slow on large Marketo instances: Marketo scans instance-wide web traffic.
var webTypeNames = []string{"Visit Webpage", "Click Link"}

func isWebType(n string) bool {
	return slices.ContainsFunc(webTypeNames, func(w string) bool { return strings.EqualFold(w, strings.TrimSpace(n)) })
}

// dropWebTypes removes Visit Webpage and Click Link unless web is set, and with web adds
// them when the list lacks them (the defaults have none). note is non-empty when it
// removed any.
func dropWebTypes(names []string, web bool) (kept []string, note string, err error) {
	if web {
		kept = slices.Clone(names)
		for _, w := range webTypeNames {
			if !slices.ContainsFunc(kept, func(n string) bool { return strings.EqualFold(strings.TrimSpace(n), w) }) {
				kept = append(kept, w)
			}
		}
		return kept, "", nil
	}
	kept = slices.DeleteFunc(slices.Clone(names), isWebType)
	if len(kept) == 0 && len(names) > 0 {
		return nil, "", errors.New("no activity types left to fetch: activity_types only has web types; add --web to include them")
	}
	if len(kept) < len(names) {
		note = "note: skipping web visits and link clicks (slow on large Marketo instances); add --web to include them"
	}
	return kept, note, nil
}

// warnUnknownTypes reports activity types this instance lacks. A user's own list warns; a
// defaulted one (some default names are unverified) only gets --verbose notes, plus one
// for a migrated v0.1.0 list.
func warnUnknownTypes(cmd *cobra.Command, env Env, cfg *config.Config, cfgPath string, unknown []string, warn func(name string)) {
	if !cfg.Marketo.ActivityTypesDefaulted {
		for _, n := range unknown {
			warn(strings.TrimSpace(n))
		}
		return
	}
	if v, _ := cmd.Flags().GetBool("verbose"); !v {
		return
	}
	if cfg.Marketo.ActivityTypesMigrated {
		fmt.Fprintf(env.Stderr, "note: activity_types is the unedited v0.1.0 list; using the 0.2.0 default activity types instead (delete activity_types from %s to drop this note)\n", cfgPath)
	}
	if len(unknown) > 0 {
		fmt.Fprintf(env.Stderr, "note: default activity types not on this instance: %s\n", strings.Join(unknown, ", "))
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
	case errors.Is(err, marketo.ErrAuth):
		// Name where each credential came from: either one could be the wrong one.
		var env, file []string
		for _, k := range []string{"client_id", "client_secret"} {
			if v := "MKTO_" + strings.ToUpper(k); cfg.FromEnv[v] {
				env = append(env, v)
			} else {
				file = append(file, k)
			}
		}
		var hints []string
		if len(env) > 0 {
			hints = append(hints, strings.Join(env, " / ")+" (set in the environment)")
		}
		if len(file) > 0 {
			hints = append(hints, strings.Join(file, " and ")+" in "+cfgPath)
		}
		return fmt.Errorf("%w: check %s", err, strings.Join(hints, " and "))
	// shortcut: matches the marketo wording, not a sentinel; TestBaseURLSourceHint catches a rename. Upgrade to a typed error if more callers need it.
	case strings.Contains(err.Error(), "check marketo.base_url"):
		if cfg.FromEnv["MKTO_BASE_URL"] {
			return fmt.Errorf("%w (base_url comes from MKTO_BASE_URL, set in the environment)", err)
		}
		return fmt.Errorf("%w (base_url is in %s)", err, cfgPath)
	}
	return err
}

// checkKey checks the lookup argument before any quota is spent: an email unless --by
// names another field, and always one value (Marketo reads a comma as a second value).
func checkKey(by, value string) error {
	if strings.EqualFold(by, "email") {
		return checkEmail(value)
	}
	if by == "" {
		return exitError{2, errors.New("--by needs a field name, e.g. --by sfdcContactId")}
	}
	if value == "" || strings.Contains(value, ",") {
		return exitError{2, fmt.Errorf("--by %s needs one value, e.g. mkto lead 1001 --by id", by)}
	}
	return nil
}

// lookupName names what was looked up, for messages: the email itself, or "sfdcContactId 003…".
func lookupName(by, value string) string {
	if strings.EqualFold(by, "email") {
		return value
	}
	return by + " " + value
}

// checkEmail rejects arguments that can't be an email before any quota is spent. It is a
// format check only, loose on purpose: plus-addresses, subdomains and IDNs pass. The
// domain needs a dot because jane@example is almost always a typo.
func checkEmail(s string) error {
	local, domain, _ := strings.Cut(s, "@")
	// A comma would become a second filter value; control characters mean a broken export
	// (UTF-16, NULs); 254 is the longest valid address and keeps one email under the URL budget.
	if local == "" || !strings.Contains(domain, ".") || strings.Contains(domain, "@") || len(s) > 254 ||
		strings.ContainsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) || r == ',' }) {
		return exitError{2, fmt.Errorf("%q doesn't look like an email address", s)}
	}
	return nil
}

// lookupNames fetches each distinct id once, in order, at most cap of them. Lookups are
// cosmetic, so a failure is counted and the first one returned; a quota or ctx error
// (lookupFatal) stops at once and is returned instead.
func lookupNames[K comparable, V any](ctx context.Context, ids []K, cap int, fetch func(K) (V, error)) (map[K]V, int, error) {
	got := map[K]V{}
	tried := map[K]bool{}
	failed, firstErr := 0, error(nil)
	for _, id := range ids {
		if tried[id] {
			continue
		}
		if len(tried) == cap {
			break
		}
		tried[id] = true
		v, err := fetch(id)
		switch {
		case err == nil:
			got[id] = v
		case lookupFatal(ctx, err):
			return got, failed, err
		default:
			failed++
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return got, failed, firstErr
}

// lookupFatal: the lookup error must end the run, not degrade a name.
func lookupFatal(ctx context.Context, err error) bool {
	return err != nil && (errors.Is(err, marketo.ErrQuotaExhausted) || ctx.Err() != nil)
}
