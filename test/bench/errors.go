package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
)

// classifyError maps a transport or response failure to the stable classes the
// parity fixtures compare. The Python harness records exception type names;
// this is the Go equivalent.
func classifyError(err error) string {
	if err == nil {
		return ""
	}
	var status *statusError
	if errors.As(err, &status) {
		return fmt.Sprintf("http_%d", status.Code)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout"
	}
	if errors.Is(err, io.EOF) {
		return "connection_reset"
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "connection reset by peer"),
		strings.Contains(message, "broken pipe"),
		strings.Contains(message, "server closed"),
		strings.Contains(message, "unexpected eof"):
		return "connection_reset"
	case strings.Contains(message, "connection refused"):
		return "connection_refused"
	case strings.Contains(message, "decode response"):
		return "decode"
	case strings.Contains(message, "unexpected prometheus response"),
		strings.Contains(message, "unexpected tempo response"),
		strings.Contains(message, "unexpected loki response"),
		strings.Contains(message, "gateway returned an error"):
		return "unexpected_response"
	default:
		return "other"
	}
}

// pythonErrorClass maps the exception names recorded by the Python harness to
// the same classes. It is only used by the parity test.
func pythonErrorClass(message string) string {
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "http error 429"):
		return "http_429"
	case strings.Contains(lower, "http error 500"):
		return "http_500"
	case strings.Contains(lower, "http error 503"):
		return "http_503"
	case strings.Contains(lower, "connection reset"),
		strings.Contains(lower, "remote end closed"),
		strings.Contains(lower, "remotedisconnected"),
		strings.Contains(lower, "eof"):
		return "connection_reset"
	case strings.Contains(lower, "timed out"), strings.Contains(lower, "timeout"):
		return "timeout"
	case strings.Contains(lower, "connection refused"):
		return "connection_refused"
	case strings.Contains(lower, "json"), strings.Contains(lower, "decode"):
		return "decode"
	default:
		return "other"
	}
}
