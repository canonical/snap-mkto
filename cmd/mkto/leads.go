// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/canonical/snap-mkto/internal/marketo"
	"github.com/canonical/snap-mkto/internal/output"
	"github.com/canonical/snap-mkto/internal/story"
)

const (
	maxFilterValues = 300  // Marketo's limit per leads.json call
	leadsDays       = 14   // the --activity window
	maxURL          = 7000 // Marketo answers 414 above 8 KB; GET only, so batches stay small
	pageTokenRoom   = 200  // room in the URL for nextPageToken on later pages
	leadsURLPath    = "/rest/v1/leads.json"
)

func newLeadsCmd(env Env) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "leads --from FILE|-",
		Short: "Look up many leads by email in few API calls",
		Long: "Look up many leads by email: score, status, owner and last update, one row per email.\n\n" +
			"FILE has one email per line, or emails in the first CSV column; lines without @ (headers)\n" +
			"are skipped. - reads stdin. Up to 300 emails cost one API call. Emails with no lead get a\n" +
			"\"not found\" row; an email shared by several leads gets one row per lead, marked (dup).\n" +
			"--activity adds each lead's last touch and 14-day touch counts. Each batch of 30 leads and\n" +
			"4 activity types costs a paging-token call plus one call per page; the 10 default types\n" +
			"make 3 batches, run at the same time; --web adds 2 types.\n" +
			"The mkto snap can read only non-hidden files in your home folder.",
		Args: func(c *cobra.Command, a []string) error { return usageErr(cobra.NoArgs(c, a)) },
		RunE: func(cmd *cobra.Command, _ []string) error { return runLeads(cmd, env) },
	}
	f := cmd.Flags()
	f.String("from", "", "read emails from FILE (one per line or the first CSV column); - reads stdin")
	f.Bool("activity", false, "add last touch and 14-day touch counts (about 6 more API calls per 30 leads, plus extra pages)")
	f.Bool("web", false, "with --activity, include Visit Webpage and Click Link (slow on large Marketo instances)")
	f.Bool("csv", false, "output CSV")
	f.String("out", "", "write the output to FILE (mode 0600) instead of stdout")
	f.Bool("force", false, "overwrite an existing --out file")
	return cmd
}

func runLeads(cmd *cobra.Command, env Env) (err error) {
	f := cmd.Flags()
	from, _ := f.GetString("from")
	asCSV, _ := f.GetBool("csv")
	asJSON, _ := f.GetBool("json")
	outPath, _ := f.GetString("out")
	withActs, _ := f.GetBool("activity")
	switch {
	case from == "":
		return exitError{2, errors.New("--from needs a file of email addresses, or - for stdin")}
	case asCSV && asJSON:
		return exitError{2, errors.New("--csv and --json can't be used together")}
	case f.Changed("web") && !withActs:
		return exitError{2, errors.New("--web only matters with --activity")}
	}
	emails, err := readEmails(env, from)
	if err != nil {
		return err
	}
	var out *os.File
	if outPath != "" {
		force, _ := f.GetBool("force")
		if out, err = openOut(env, outPath, force); err != nil {
			return err
		}
		defer func() {
			if err != nil { // leave no empty or partial file of mkto's own behind
				out.Close()
				os.Remove(out.Name())
			}
		}()
	}

	cfg, cfgPath, client, err := newClient(cmd, env)
	if err != nil {
		return err
	}
	loc, err := cfg.Location()
	if err != nil {
		return err
	}
	fields := slices.Clone(cfg.Marketo.LeadFields)
	for _, k := range append([]string{"id", "email", "updatedAt"}, cfg.Marketo.ProfileFields...) {
		if !slices.Contains(fields, k) {
			fields = append(fields, k)
		}
	}
	verbose, _ := f.GetBool("verbose")
	byEmail := map[string][]marketo.Lead{}
	for _, chunk := range chunkEmails(emails, maxURL-pageTokenRoom-len(cfg.Marketo.BaseURL+leadsURLPath+"?"+
		url.Values{"filterType": {"email"}, "filterValues": {""}, "fields": {strings.Join(fields, ",")}}.Encode())) {
		for {
			leads, err := client.Leads(cmd.Context(), "email", chunk, fields)
			// A missing profile field is skipped for the rest of the run, as in lead; the
			// lookup key and lead_fields are never dropped.
			fe, ok := errors.AsType[*marketo.FieldError](err)
			i := -1
			if ok && !inLeadFields(cfg, fe.Field) && !strings.EqualFold(fe.Field, "id") && !strings.EqualFold(fe.Field, "email") {
				i = slices.IndexFunc(fields, func(k string) bool { return strings.EqualFold(k, fe.Field) })
			}
			if i >= 0 {
				fields = slices.Delete(fields, i, i+1)
				if verbose {
					fmt.Fprintf(env.Stderr, "note: field %q isn't on this Marketo instance; skipped\n", output.Clean(fe.Field))
				}
				continue
			}
			if err != nil {
				return mapErr(err, cfg, cfgPath)
			}
			for _, l := range leads {
				k := strings.ToLower(fmt.Sprint(l["email"]))
				byEmail[k] = append(byEmail[k], l)
			}
			break
		}
	}
	var sums map[int]story.Summary
	if withActs {
		var ids []int
		seen := map[int]bool{}
		for _, e := range emails {
			for _, l := range byEmail[strings.ToLower(e)] {
				if !seen[l.ID()] {
					seen[l.ID()] = true
					ids = append(ids, l.ID())
				}
			}
		}
		web, _ := f.GetBool("web")
		if sums, err = leadTouches(cmd, env, cfg, cfgPath, client, ids, env.Now().Add(-leadsDays*24*time.Hour), web); err != nil {
			return err
		}
	}

	var buf bytes.Buffer
	if asJSON {
		res := output.LeadsJSON{Leads: []output.LeadJSON{}, NotFound: []string{}}
		for _, e := range emails {
			if len(byEmail[strings.ToLower(e)]) == 0 {
				res.NotFound = append(res.NotFound, e)
			}
			for _, l := range byEmail[strings.ToLower(e)] {
				lj := leadJSON(cfg, l, loc)
				lj.LastTouch, lj.Touches = sums[l.ID()].LastTouch.UTC(), sums[l.ID()].Counts
				res.Leads = append(res.Leads, lj)
			}
		}
		err = output.WriteJSON(&buf, res)
	} else {
		rows := [][]string{{"EMAIL", "ID", "SCORE", "STATUS", "OWNER", "UPDATED"}}
		if withActs {
			rows[0] = append(rows[0], "LAST TOUCH", fmt.Sprintf("%dD", leadsDays))
		}
		for _, e := range emails {
			leads := byEmail[strings.ToLower(e)]
			if len(leads) == 0 {
				rows = append(rows, append([]string{e, "not found"}, make([]string, len(rows[0])-2)...))
			}
			if len(leads) > 1 {
				e += " (dup)"
			}
			for _, l := range leads {
				row := []string{e, strconv.Itoa(l.ID()), display(l["leadScore"], loc), display(l["leadStatus"], loc),
					owner(l, loc), display(l["updatedAt"], loc)}
				if withActs {
					row = append(row, touchCells(sums[l.ID()], loc, env.Now())...)
				}
				rows = append(rows, row)
			}
		}
		err = writeRows(&buf, rows, asCSV)
	}
	if err != nil {
		return err
	}
	return finishOut(env, out, outPath, buf.Bytes())
}

// writeRows renders rows as CSV, or as a table with "-" for empty cells.
func writeRows(buf *bytes.Buffer, rows [][]string, asCSV bool) error {
	if !asCSV {
		for _, r := range rows {
			for i, c := range r {
				if c == "" {
					r[i] = "-"
				}
			}
		}
		return output.Table(buf, rows)
	}
	w := csv.NewWriter(buf)
	for _, r := range rows {
		for i, c := range r {
			r[i] = csvSafe(output.Clean(c)) // Clean first: Marketo text must not drive the terminal
		}
		_ = w.Write(r) // errors surface from Flush via w.Error
	}
	w.Flush()
	return w.Error()
}

// finishOut writes b to stdout, or to out (from openOut) and then outPath, and says where.
func finishOut(env Env, out *os.File, outPath string, b []byte) (err error) {
	if outPath == "" {
		_, err = env.Stdout.Write(b)
		return err
	}
	_, err = out.Write(b)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && out.Name() != outPath { // --force: replace the target only now that the run succeeded
		err = os.Rename(out.Name(), outPath)
	}
	if err != nil {
		return err
	}
	abs, aerr := filepath.Abs(outPath)
	if aerr != nil {
		abs = outPath
	}
	fmt.Fprintln(env.Stderr, output.Clean("wrote "+abs))
	if note := snapTmpNote(env.Getenv("SNAP_NAME"), abs); note != "" {
		fmt.Fprintln(env.Stderr, output.Clean(note))
	}
	return nil
}

// readEmails returns the valid emails in from (a path, or - for stdin), trimmed and
// de-duplicated ignoring case, in input order. Skipped lines are counted on stderr, never
// named: the file holds lead emails.
func readEmails(env Env, from string) ([]string, error) {
	r := env.Stdin
	if from != "-" {
		file, err := os.Open(from)
		if err != nil {
			if env.Getenv("SNAP_NAME") != "" && errors.Is(err, fs.ErrPermission) {
				return nil, fmt.Errorf("%w; the mkto snap can read only non-hidden paths in your home folder (home plug)", err)
			}
			return nil, err
		}
		defer file.Close()
		r = file
	}
	// Caps keep a runaway input from eating memory or the daily API quota.
	in := &countReader{r: io.LimitReader(r, maxLeadsInput+1)}
	br := bufio.NewReader(in)
	if b, _ := br.Peek(3); bytes.Equal(b, []byte("\xef\xbb\xbf")) { // spreadsheet exports start with a BOM
		br.Discard(3)
	}
	cr := csv.NewReader(br)
	cr.FieldsPerRecord = -1 // rows may differ in width; only the first column counts
	cr.LazyQuotes = true
	var emails []string
	seen := map[string]bool{}
	noAt, invalid := 0, 0
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, exitError{2, fmt.Errorf("reading the email list: %w", err)} // csv errors name a line, not its content
		}
		e := strings.TrimSpace(rec[0])
		switch {
		case e == "":
		case !strings.Contains(e, "@"):
			noAt++
		case checkEmail(e) != nil:
			invalid++
		case !seen[strings.ToLower(e)]:
			seen[strings.ToLower(e)] = true
			emails = append(emails, e)
			if len(emails) > maxLeadEmails {
				return nil, exitError{2, fmt.Errorf("the --from input has more than %d emails (over %d API calls); split the list into smaller files", maxLeadEmails, maxLeadEmails/300)}
			}
		}
	}
	if in.n > maxLeadsInput {
		return nil, exitError{2, fmt.Errorf("the --from input is larger than %d MB; split the list into smaller files", maxLeadsInput>>20)}
	}
	if noAt > 0 {
		fmt.Fprintf(env.Stderr, "note: skipped %s without @ (headers or blank cells)\n", count(noAt, "line", "lines"))
	}
	if invalid > 0 {
		fmt.Fprintf(env.Stderr, "note: skipped %s; check the list\n", count(invalid, "invalid email address", "invalid email addresses"))
	}
	if len(emails) == 0 {
		return nil, exitError{2, errors.New("no email addresses to look up in the --from input")}
	}
	return emails, nil
}

func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// chunkEmails splits emails into calls of at most 300 values whose URL-encoded,
// comma-joined filterValues stays within budget bytes.
func chunkEmails(emails []string, budget int) [][]string {
	var chunks [][]string
	var cur []string
	size := 0
	for _, e := range emails {
		n := len(url.QueryEscape(e))
		if len(cur) > 0 {
			n += len("%2C")
		}
		if len(cur) == maxFilterValues || (len(cur) > 0 && size+n > budget) {
			chunks, cur, size = append(chunks, cur), nil, 0
			n -= len("%2C")
		}
		cur, size = append(cur, e), size+n
	}
	return append(chunks, cur)
}

// csvSafe stops a spreadsheet from running a cell as a formula: Marketo values are
// not trusted. Numbers such as -5 are left alone.
func csvSafe(s string) string {
	if s == "" || !strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return s
	}
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return s
	}
	return "'" + s
}

const (
	maxLeadEmails = 30000    // 100 lookup calls at 300 per call
	maxLeadsInput = 10 << 20 // bytes
)

// countReader counts the bytes read through it.
type countReader struct {
	r io.Reader
	n int64
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
