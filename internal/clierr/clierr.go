package clierr

import (
	"errors"
	"fmt"

	"github.com/harshalranjhani/preview-cli/internal/exitcode"
)

// Error is a user-facing failure with a stable code and process exit status.
type Error struct {
	Code    string
	Message string
	Exit    int
}

func (e *Error) Error() string { return e.Message }

// New builds a user-facing error. Exit defaults to 1 when unset.
func New(exit int, code, message string) *Error {
	if exit == 0 {
		exit = exitcode.Generic
	}
	if code == "" {
		code = "ERROR"
	}
	return &Error{Exit: exit, Code: code, Message: message}
}

// Wrap attaches a stable code to an underlying error.
func Wrap(exit int, code string, err error) *Error {
	if err == nil {
		return nil
	}
	return New(exit, code, err.Error())
}

// ExitOf returns the process status for err.
func ExitOf(err error) int {
	if err == nil {
		return exitcode.OK
	}
	var e *Error
	if errors.As(err, &e) && e.Exit != 0 {
		return e.Exit
	}
	return exitcode.Generic
}

// CodeOf returns the stable error code for err.
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) && e.Code != "" {
		return e.Code
	}
	return "ERROR"
}

// Usage is an invalid arguments or configuration error.
func Usage(format string, args ...any) *Error {
	return New(exitcode.Usage, "INVALID_ARGUMENT", fmt.Sprintf(format, args...))
}

// TargetUnreachable means the local application is not accepting connections.
func TargetUnreachable(host string, port int) *Error {
	return New(exitcode.Target, "TARGET_UNREACHABLE", fmt.Sprintf("nothing appears to be listening on %s:%d", host, port))
}
