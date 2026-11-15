package gateway

import (
	"testing"
	"time"
)

func TestShutdownTimeoutCoversQueryBudget(t *testing.T) {
	if got := shutdownTimeout(30 * time.Second); got != 35*time.Second {
		t.Fatalf("shutdownTimeout(30s) = %s, want 35s", got)
	}
	if got := shutdownTimeout(0); got != 10*time.Second {
		t.Fatalf("shutdownTimeout(0) = %s, want the 10s floor", got)
	}
}
