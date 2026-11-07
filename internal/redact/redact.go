// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

// Package redact is the only path from Marketo data to an LLM. It builds
// payloads from an allowlist of fields, redacts personal data left in them in
// place, and runs a fail-closed guard over the result.
package redact

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/canonical/snap-mkto/internal/marketo"
)

// Event is one redacted activity.
type Event struct {
	ID    string    `json:"id"`   // E1..En, oldest first
	Date  time.Time `json:"date"` // truncated to the hour, UTC
	Type  string    `json:"type,omitempty"`
	Asset string    `json:"asset,omitempty"`
	URL   string    `json:"url,omitempty"`
}

type profile struct {
	Company           string `json:"company,omitempty"`
	Industry          string `json:"industry,omitempty"`
	Country           string `json:"country,omitempty"`
	NumberOfEmployees int    `json:"numberOfEmployees,omitempty"`
	Role              string `json:"role,omitempty"`
}

// Payload is a redacted lead profile and timeline. Only Build makes one.
type Payload struct {
	lead       profile
	events     []Event
	redactions []Redaction
}

// Redaction says what kind of data was replaced with {redacted}, and where
// (a JSON path such as events[0].asset). It never holds the value.
type Redaction struct{ Kind, Path string }

// CompanyQuery is what company research may see. Only Payload.Company makes one.
type CompanyQuery struct {
	company, industry, country string
}

// Activity types whose primary value is an asset name (email, form, program).
var assetTypes = map[string]bool{
	"open email": true, "click email": true, "email delivered": true, "unsubscribe email": true,
	"fill out form": true, "change status in progression": true, "new lead": true,
}

// Activity types whose primary value is a URL.
var urlTypes = map[string]bool{"visit webpage": true, "click link": true}

// Build redacts a lead and its activities. types maps activity type id to name.
// Personal data left in a value (an email, phone, IP or the lead's name) is
// replaced in place with {redacted}; Build fails only if the guard still finds
// some after that.
func Build(l marketo.Lead, acts []marketo.Activity, types map[int]string) (Payload, error) {
	p := Payload{lead: profile{
		Company:  field(l, "company"),
		Industry: field(l, "industry"),
		Country:  field(l, "country"),
		Role:     TitleBucket(field(l, "title")),
	}}
	if n, err := strconv.Atoi(strings.TrimSpace(fmt.Sprint(l["numberOfEmployees"]))); err == nil && n >= 1 && n <= 10_000_000 {
		p.lead.NumberOfEmployees = n // the cap keeps it below the guard's 9-digit phone rule
	}

	sorted := append([]marketo.Activity(nil), acts...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if !sorted[i].Date.Equal(sorted[j].Date) {
			return sorted[i].Date.Before(sorted[j].Date)
		}
		return sorted[i].ID < sorted[j].ID
	})
	for i, a := range sorted {
		name, ok := types[a.TypeID]
		if !ok {
			name = fmt.Sprintf("activity type %d", a.TypeID)
		}
		e := Event{ID: fmt.Sprintf("E%d", i+1), Date: a.Date.UTC().Truncate(time.Hour), Type: name}
		switch kind := strings.ToLower(strings.TrimSpace(name)); {
		case assetTypes[kind]:
			e.Asset = strings.TrimSpace(a.Primary)
		case urlTypes[kind]:
			raw := strings.TrimSpace(a.Primary)
			if u, err := url.Parse(raw); raw != "" && (err != nil || u.Host == "") {
				raw = "https://" + raw // Visit Webpage sends "www.example.com/pricing"
			}
			e.URL = ScrubURL(raw)
		}
		p.events = append(p.events, e)
	}

	d := newDetector(l)
	var err error
	add := func(r Redaction) {
		if !slices.Contains(p.redactions, r) {
			p.redactions = append(p.redactions, r)
		}
	}
	for pass := range 3 { // one pass should do; more cover a replacement that exposes a new hit
		for _, lf := range p.leaves() {
			if pass == 0 && strings.HasSuffix(lf.path, ".url") && strings.Contains(*lf.s, "{redacted}") {
				add(Redaction{"email or token in URL", lf.path}) // ScrubURL replaced a segment
			}
			var kinds []string
			*lf.s, kinds = redactFn(d, lf.path, *lf.s)
			for _, k := range kinds {
				add(Redaction{k, lf.path})
			}
		}
		if err = d.guard(p.JSON()); err == nil {
			return p, nil
		}
	}
	return Payload{}, err
}

// leaf is one string value of the payload and its JSON path.
type leaf struct {
	path string
	s    *string
}

func (p *Payload) leaves() []leaf {
	ls := []leaf{{"lead.company", &p.lead.Company}, {"lead.industry", &p.lead.Industry},
		{"lead.country", &p.lead.Country}, {"lead.role", &p.lead.Role}}
	for i := range p.events {
		e, pre := &p.events[i], fmt.Sprintf("events[%d].", i)
		ls = append(ls, leaf{pre + "id", &e.ID}, leaf{pre + "type", &e.Type}, leaf{pre + "asset", &e.Asset}, leaf{pre + "url", &e.URL})
	}
	return ls
}

// Redactions lists what Build replaced, in payload order; empty if nothing was.
func (p Payload) Redactions() []Redaction { return slices.Clone(p.redactions) }

// Company returns the company research query: company, industry and country
// from the already-redacted payload, nothing else.
func (p Payload) Company() CompanyQuery {
	return CompanyQuery{p.lead.Company, p.lead.Industry, p.lead.Country}
}

// Events returns a copy of the payload's events, oldest first. It is never nil.
func (p Payload) Events() []Event { return append([]Event{}, p.events...) }

// Name returns the company name; empty if the lead has none.
func (q CompanyQuery) Name() string { return q.company }

// JSON returns the payload as deterministic JSON; empty values are omitted.
func (p Payload) JSON() []byte {
	return marshal(struct {
		Lead   profile `json:"lead"`
		Events []Event `json:"events,omitempty"`
	}{p.lead, p.events})
}

// JSON returns the query as deterministic JSON; empty values are omitted.
func (q CompanyQuery) JSON() []byte {
	return marshal(struct {
		Company  string `json:"company,omitempty"`
		Industry string `json:"industry,omitempty"`
		Country  string `json:"country,omitempty"`
	}{q.company, q.industry, q.country})
}

func marshal(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false) // keep "&" and ">" readable in --show-payload
	if err := enc.Encode(v); err != nil {
		panic(err) // only strings, ints and times: cannot fail
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n"))
}

func field(l marketo.Lead, key string) string {
	s, _ := l[key].(string)
	return strings.TrimSpace(s)
}
