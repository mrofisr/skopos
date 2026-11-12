package gateway

import (
	"strings"
	"testing"

	"github.com/jawaracloud/skopos/internal/config"
)

func TestWorstCaseBufferingWarning(t *testing.T) {
	const mib = 1 << 20
	tests := []struct {
		name        string
		cfg         config.Config
		memoryLimit int64
		want        string
	}{
		{
			name:        "over the limit",
			cfg:         config.Config{MaxInflight: 4, MaxResponseBytes: 64 * mib},
			memoryLimit: 128 * mib,
			want:        "worst-case buffering",
		},
		{
			name:        "under the limit",
			cfg:         config.Config{MaxInflight: 4, MaxResponseBytes: 16 * mib},
			memoryLimit: 128 * mib,
		},
		{
			name:        "equal is safe",
			cfg:         config.Config{MaxInflight: 4, MaxResponseBytes: 32 * mib},
			memoryLimit: 128 * mib,
		},
		{
			name:        "admission disabled",
			cfg:         config.Config{MaxResponseBytes: 64 * mib},
			memoryLimit: 1 * mib,
		},
		{
			name:        "unknown limit",
			cfg:         config.Config{MaxInflight: 4, MaxResponseBytes: 64 * mib},
			memoryLimit: 0,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := worstCaseBufferingWarning(test.cfg, test.memoryLimit)
			if test.want == "" {
				if got != "" {
					t.Fatalf("warning = %q, want none", got)
				}
				return
			}
			if !strings.Contains(got, test.want) {
				t.Fatalf("warning = %q, want %q", got, test.want)
			}
		})
	}
}
