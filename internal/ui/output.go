package ui

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/harshalranjhani/preview-cli/internal/clierr"
)

// WriteJSON prints indented JSON.
func WriteJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// ErrorBody is the machine-readable error document.
type ErrorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// PrintError writes a failure for humans or agents.
func PrintError(jsonMode bool, stdout, stderr io.Writer, err error) {
	msg := err.Error()
	if jsonMode {
		body := ErrorBody{}
		body.Error.Code = clierr.CodeOf(err)
		body.Error.Message = msg
		_ = WriteJSON(stdout, body)
	}
	fmt.Fprintf(stderr, "Error: %s\n", msg)
}

// FormatDuration renders a lifetime the way the CLI shows it to people.
func FormatDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	if d%(24*time.Hour) == 0 {
		days := int(d / (24 * time.Hour))
		if days > 0 {
			return fmt.Sprintf("%dd", days)
		}
	}
	if d >= time.Hour {
		h := int(d / time.Hour)
		m := int((d % time.Hour) / time.Minute)
		if m == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh %dm", h, m)
	}
	if d >= time.Minute {
		return fmt.Sprintf("%dm", int(d/time.Minute))
	}
	return fmt.Sprintf("%ds", int(d/time.Second))
}

// FormatRemaining describes how long a preview has left.
func FormatRemaining(expires *time.Time, now time.Time) string {
	if expires == nil {
		return "never"
	}
	if !expires.After(now) {
		return "expired"
	}
	return FormatDuration(expires.Sub(now).Truncate(time.Second))
}

// Truncate shortens s for table cells.
func Truncate(s string, max int) string {
	if max <= 1 || len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

// Debugf prints a troubleshooting line to stderr when enabled.
func Debugf(enabled bool, format string, args ...any) {
	if !enabled {
		return
	}
	fmt.Fprintf(os.Stderr, "debug: %s\n", fmt.Sprintf(format, args...))
}
