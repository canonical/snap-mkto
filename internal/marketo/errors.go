// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package marketo

import (
	"errors"
	"fmt"
)

// ErrQuotaExhausted is Marketo error 607: the daily API quota is used up.
var ErrQuotaExhausted = errors.New("Marketo daily API quota reached (607); it resets at 12:00 AM US Central. Run `mkto quota` to see usage")

// APIError is a Marketo error code mkto has no special handling for.
type APIError struct{ Code, Message string }

func (e *APIError) Error() string { return fmt.Sprintf("Marketo error %s: %s", e.Code, e.Message) }

// FieldError is Marketo error 1006: a requested lead field does not exist.
type FieldError struct{ Field string }

func (e *FieldError) Error() string {
	return fmt.Sprintf("field %q not found: fix lead_fields in your config file", e.Field)
}
