// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import "errors"

// exitError carries a process exit code: 0 ok, 1 error, 2 usage or not
// implemented, 3 lead not found, 4 daily quota exhausted.
type exitError struct {
	code int
	err  error
}

func (e exitError) Error() string { return e.err.Error() }
func (e exitError) Unwrap() error { return e.err }

func exitCode(err error) int {
	var ee exitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ee):
		return ee.code
	}
	return 1
}

// usageErr wraps flag and argument errors, which cobra does not type.
// It keeps nil as nil so it can wrap a validator's result directly.
func usageErr(err error) error {
	if err == nil {
		return nil
	}
	return exitError{2, err}
}
