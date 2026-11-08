// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"cmp"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/canonical/snap-mkto/internal/marketo"
	"github.com/canonical/snap-mkto/internal/output"
)

func newTypesCmd(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "types",
		Short: "List activity types (names for activity_types and --types)",
		Args:  func(c *cobra.Command, a []string) error { return usageErr(cobra.NoArgs(c, a)) },
		RunE:  func(cmd *cobra.Command, _ []string) error { return runTypes(cmd, env) },
	}
	cmd.Flags().String("search", "", "show only types whose name or an attribute name contains this text (case-insensitive)")
	return cmd
}

func runTypes(cmd *cobra.Command, env Env) error {
	search, _ := cmd.Flags().GetString("search")
	asJSON, _ := cmd.Flags().GetBool("json")
	cfg, cfgPath, client, err := newClient(cmd, env)
	if err != nil {
		return err
	}
	all, err := client.ActivityTypes(cmd.Context())
	if err != nil {
		return mapErr(err, cfg, cfgPath)
	}
	slices.SortFunc(all, func(a, b marketo.ActivityType) int { return cmp.Compare(a.ID, b.ID) })
	s := strings.ToLower(search)
	var rows [][]string
	list := []output.TypeJSON{}
	for _, t := range all {
		if !slices.ContainsFunc(append([]string{t.Name}, t.Attributes...), func(n string) bool { return strings.Contains(strings.ToLower(n), s) }) {
			continue
		}
		list = append(list, output.TypeJSON{ID: t.ID, Name: t.Name, Description: t.Description, Primary: t.Primary, Attributes: t.Attributes})
		// "-" for none, so the last column never leaves trailing spaces
		rows = append(rows, []string{fmt.Sprint(t.ID), t.Name, t.Primary, cmp.Or(strings.Join(t.Attributes, ", "), "-")})
	}
	var buf bytes.Buffer
	switch {
	case asJSON:
		err = output.WriteJSON(&buf, list)
	case len(rows) == 0:
		fmt.Fprintf(&buf, "No activity types match %q.\n", search)
	default:
		err = output.Table(&buf, append([][]string{{"ID", "NAME", "PRIMARY", "ATTRIBUTES"}}, rows...))
	}
	if err != nil {
		return err
	}
	_, err = env.Stdout.Write(buf.Bytes())
	return err
}

// closestType suggests a known type for a misspelt name: the nearest by edit distance
// (at most 3) or containing it either way, ties broken alphabetically; "" if none.
func closestType(all []marketo.ActivityType, name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return ""
	}
	best, bestD := "", 0
	for _, t := range all {
		l := strings.ToLower(t.Name)
		d := levenshtein(n, l)
		// Substring matches count only from 4 letters, so "e" suggests nothing.
		sub := min(utf8.RuneCountInString(n), utf8.RuneCountInString(l)) >= 4 && (strings.Contains(l, n) || strings.Contains(n, l))
		if d > 3 && !sub {
			continue
		}
		if best == "" || d < bestD || (d == bestD && t.Name < best) {
			best, bestD = t.Name, d
		}
	}
	return best
}

// levenshtein is the edit distance between a and b, counted in runes.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := range ra {
		cur := []int{i + 1}
		for j := range rb {
			sub := prev[j]
			if ra[i] != rb[j] {
				sub++
			}
			cur = append(cur, min(prev[j+1]+1, cur[j]+1, sub))
		}
		prev = cur
	}
	return prev[len(rb)]
}
