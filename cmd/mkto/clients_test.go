// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/canonical/snap-mkto/internal/marketo"
)

func TestLookupNamesCap(t *testing.T) {
	ctx := context.Background()
	ids := []int{1, 1, 2, 3, 4, 5}
	var calls []int
	got, failed, err := lookupNames(ctx, ids, 3, func(id int) (string, error) {
		calls = append(calls, id)
		if id == 2 {
			return "", errors.New("no program 2")
		}
		return fmt.Sprint("P", id), nil
	})
	if fmt.Sprint(calls) != "[1 2 3]" || failed != 1 || err == nil || err.Error() != "no program 2" || len(got) != 2 || got[3] != "P3" {
		t.Errorf("calls %v, got %v, failed %d, err %v", calls, got, failed, err)
	}
	calls = nil
	quota := fmt.Errorf("wrapped: %w", marketo.ErrQuotaExhausted)
	_, _, err = lookupNames(ctx, ids, 25, func(id int) (string, error) {
		calls = append(calls, id)
		if id == 1 {
			return "", errors.New("cosmetic")
		}
		return "", quota
	})
	if !errors.Is(err, marketo.ErrQuotaExhausted) || len(calls) != 2 {
		t.Errorf("quota: calls %v, err %v; want a stop after the quota error, returning it", calls, err)
	}
}
