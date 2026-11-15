package scale

import (
	"fmt"
	"time"
)

// Profile is one synthetic dataset shape. Series are spread evenly across
// Services, and each series has Samples metric points over Range at Step.
type Profile struct {
	Name     string
	Series   int
	Services int
	Samples  int
	Logs     int
	Spans    int
	Step     time.Duration
	Range    time.Duration
}

const (
	Profile10k  = "10k"
	Profile100k = "100k"
)

// Lookup returns a built-in profile by name.
func Lookup(name string) (Profile, error) {
	switch name {
	case Profile10k:
		return Profile{
			Name:     name,
			Series:   10_000,
			Services: 10,
			Samples:  240,
			Logs:     10_000,
			Spans:    10_000,
			Step:     15 * time.Second,
			Range:    time.Hour,
		}, nil
	case Profile100k:
		return Profile{
			Name:     name,
			Series:   100_000,
			Services: 50,
			Samples:  240,
			Logs:     100_000,
			Spans:    100_000,
			Step:     15 * time.Second,
			Range:    time.Hour,
		}, nil
	default:
		return Profile{}, fmt.Errorf("scale: unknown profile %q", name)
	}
}

// Validate rejects profiles that cannot define a deterministic dataset.
func (p Profile) Validate() error {
	switch {
	case p.Series <= 0:
		return fmt.Errorf("scale: series must be positive")
	case p.Services <= 0:
		return fmt.Errorf("scale: services must be positive")
	case p.Series < p.Services:
		return fmt.Errorf("scale: series (%d) must be at least services (%d)", p.Series, p.Services)
	case p.Samples <= 0:
		return fmt.Errorf("scale: samples must be positive")
	case p.Logs < 0:
		return fmt.Errorf("scale: logs must not be negative")
	case p.Spans < 0:
		return fmt.Errorf("scale: spans must not be negative")
	case p.Step <= 0:
		return fmt.Errorf("scale: step must be positive")
	case p.Range <= 0:
		return fmt.Errorf("scale: range must be positive")
	case p.Samples != int(p.Range/p.Step):
		return fmt.Errorf("scale: samples (%d) must equal range/step (%d)", p.Samples, int(p.Range/p.Step))
	}
	return nil
}
