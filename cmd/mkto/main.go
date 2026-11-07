// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/signal"
	"time"
	_ "time/tzdata" // America/Chicago must resolve inside the snap too
)

// Set via -ldflags "-X main.version=... -X main.commit=...".
var version, commit = "dev", "none"

// Env is the injection point for everything a command touches outside itself.
type Env struct {
	Stdout, Stderr io.Writer
	Getenv         func(string) string
	HTTP           *http.Client
	Now            func() time.Time
	Sleep          func(context.Context, time.Duration) error // tests only: main leaves it nil, so marketo uses its own
	LLMBaseURL     string                                     // tests only: main leaves it empty, which means the production Anthropic API
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := runContext(ctx, os.Args[1:], Env{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Getenv: os.Getenv,
		HTTP:   &http.Client{}, // no client-wide timeout: marketo and llm set their own per request
		Now:    time.Now,
	})
	stop()
	os.Exit(code)
}
