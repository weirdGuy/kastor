package main

import (
	"errors"
	"fmt"
)

// exitError tags an error with the process exit status main should use.
// Convention (issue #38): 0 clean, 1 validation/codegen errors (the default
// for untagged errors), 2 usage/IO errors.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

// withExitCode tags err with a specific exit status, preserving the error
// chain for errors.As/Is.
func withExitCode(code int, err error) error {
	return &exitError{code: code, err: err}
}

// usageErrorf builds an exit-code-2 error for a bad invocation: wrong flags
// or arguments, or a target selection the module cannot satisfy.
func usageErrorf(format string, args ...any) error {
	return withExitCode(2, fmt.Errorf(format, args...))
}

// exitCode maps a command error to the process exit status.
func exitCode(err error) int {
	var coded *exitError
	if errors.As(err, &coded) {
		return coded.code
	}
	return 1
}
