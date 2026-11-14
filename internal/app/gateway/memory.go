package gateway

import (
	"fmt"

	"github.com/mrofisr/skopos/internal/config"
)

// worstCaseBufferingWarning reports when MaxInflight responses at the maximum
// response size would exceed the Go memory limit. An empty string means safe
// or admission is disabled.
func worstCaseBufferingWarning(cfg config.Config, memoryLimit int64) string {
	if cfg.MaxInflight <= 0 || cfg.MaxResponseBytes <= 0 || memoryLimit <= 0 {
		return ""
	}
	worstCase := int64(cfg.MaxInflight) * cfg.MaxResponseBytes
	if worstCase <= memoryLimit {
		return ""
	}
	return fmt.Sprintf(
		"worst-case buffering %d bytes (%d in-flight x %d response bytes) exceeds GOMEMLIMIT %d bytes",
		worstCase, cfg.MaxInflight, cfg.MaxResponseBytes, memoryLimit,
	)
}
