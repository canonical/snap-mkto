// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

// Package llm runs the LLM steps of mkto brief. Its exported functions accept
// only redact types, so unredacted Marketo data cannot reach a provider.
package llm

import (
	"context"
	"errors"

	"github.com/canonical/snap-mkto/internal/redact"
)

// Provider runs the narrate and verify steps against one LLM provider.
type Provider interface {
	// Narrate writes a short narrative of the payload's events, every sentence citing event ids.
	Narrate(ctx context.Context, p redact.Payload) (Narrative, Usage, error)
	// Verify returns the 0-based indexes of sentences in n that their cited events do not support.
	Verify(ctx context.Context, p redact.Payload, n Narrative) (unsupported []int, u Usage, err error)
	// Research looks the company up on the web. Every claim it returns cites at
	// least one source, and every source appeared in the web search results.
	Research(ctx context.Context, q redact.CompanyQuery) (Research, Usage, error)
	// Pitch picks at most 3 offerings from in.Playbook. Every kept offering names a playbook
	// heading and cites only events of p that in.Sentences cite, or 1-based source numbers into in.Sources.
	Pitch(ctx context.Context, p redact.Payload, in PitchInput) (Pitch, Usage, error)
}

// PitchInput holds the outputs of the earlier redact-gated steps (the checked narrative,
// the research claims and sources) and the user's playbook.md text.
type PitchInput struct {
	Sentences []Sentence `json:"sentences"`
	Claims    []Claim    `json:"research"`
	Sources   []Source   `json:"sources"`
	Playbook  string     `json:"-"` // sent in its own fenced block
}

// Pitch is the pitch step's output. NoFit is true when no offering is left.
type Pitch struct {
	Offerings []Offering `json:"offerings"`
	Opener    string     `json:"opener"`
	Unknowns  []string   `json:"unknowns"`
	NoFit     bool       `json:"no_fit"`
}

// Offering is one playbook offering. Cites holds event ids ("E3") and source numbers ("2").
type Offering struct {
	Name   string   `json:"name"`
	Reason string   `json:"reason"`
	Cites  []string `json:"cites"`
}

// Research is the research step's output. Claim source indexes are 1-based into Sources.
// Size and Industry are nil when unknown.
type Research struct {
	Summary  []Claim  `json:"summary"`
	Size     *Claim   `json:"size"`
	Industry *Claim   `json:"industry"`
	Signals  []Claim  `json:"signals"`
	News     []Claim  `json:"news"`
	Sources  []Source `json:"sources"`
}

// Claim is one research statement and the sources (1-based) that back it.
type Claim struct {
	Text    string `json:"text"`
	Sources []int  `json:"sources"`
}

// Source is a web page the research cites.
type Source struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}

// Narrative is the narrate step's output.
type Narrative struct {
	Sentences []Sentence `json:"sentences"`
}

// Sentence is one narrative sentence and the event ids (E1..En) it cites.
type Sentence struct {
	Text  string   `json:"text"`
	Cites []string `json:"cites"`
}

// Usage counts the tokens one or more steps used.
type Usage struct {
	InputTokens  int `json:"in"`
	OutputTokens int `json:"out"`
}

// Add returns the sum of u and v.
func (u Usage) Add(v Usage) Usage {
	return Usage{u.InputTokens + v.InputTokens, u.OutputTokens + v.OutputTokens}
}

// ErrRefused means the model declined a step (stop_reason "refusal"). The caller skips that step.
var ErrRefused = errors.New("the model declined this step")

// ErrResearchUnavailable means web search failed and left no results to use. The caller skips research.
var ErrResearchUnavailable = errors.New("research unavailable")
