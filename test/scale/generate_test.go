package scale

import (
	"testing"
	"time"
)

func TestProfileCounts(t *testing.T) {
	profile, err := Lookup(Profile10k)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if profile.Series != 10_000 || profile.Services != 10 || profile.Samples != 240 {
		t.Fatalf("profile = %#v, want 10000 series, 10 services, 240 samples", profile)
	}
	dataset, err := Generate(profile, 42)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if dataset.MetricSeries != 10_000 || dataset.MetricSamples != 2_400_000 {
		t.Fatalf("metric counts = %d series, %d samples", dataset.MetricSeries, dataset.MetricSamples)
	}
	if dataset.LogLines != 10_000 || dataset.Spans != 10_000 {
		t.Fatalf("logs/spans = %d/%d, want 10000/10000", dataset.LogLines, dataset.Spans)
	}
}

func TestProfile100k(t *testing.T) {
	profile, err := Lookup(Profile100k)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if profile.Series != 100_000 || profile.Services != 50 {
		t.Fatalf("profile = %#v, want 100000 series, 50 services", profile)
	}
}

func TestSeedDeterministic(t *testing.T) {
	profile, err := Lookup(Profile10k)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	first, err := Generate(profile, 42)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	second, err := Generate(profile, 42)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if first.Hash() != second.Hash() {
		t.Fatalf("same seed hashes differ: %s != %s", first.Hash(), second.Hash())
	}
	other, err := Generate(profile, 43)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if first.Hash() == other.Hash() {
		t.Fatalf("different seeds share a hash: %s", first.Hash())
	}
}

func TestHashGolden(t *testing.T) {
	profile, err := Lookup(Profile10k)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	dataset, err := Generate(profile, 42)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	const want = "89094c7a42891a57"
	if got := dataset.Hash(); got != want {
		t.Fatalf("hash = %s, want %s", got, want)
	}
}

func TestInvalidProfile(t *testing.T) {
	if _, err := Lookup("unknown"); err == nil {
		t.Fatal("Lookup(unknown) succeeded")
	}
	cases := []struct {
		name    string
		profile Profile
	}{
		{"zero series", Profile{Services: 1, Samples: 1, Step: time.Second, Range: time.Second}},
		{"zero services", Profile{Series: 1, Samples: 1, Step: time.Second, Range: time.Second}},
		{"series below services", Profile{Series: 1, Services: 2, Samples: 1, Step: time.Second, Range: time.Second}},
		{"zero samples", Profile{Series: 1, Services: 1, Step: time.Second, Range: time.Second}},
		{"negative logs", Profile{Series: 1, Services: 1, Samples: 1, Logs: -1, Step: time.Second, Range: time.Second}},
		{"negative spans", Profile{Series: 1, Services: 1, Samples: 1, Spans: -1, Step: time.Second, Range: time.Second}},
		{"zero step", Profile{Series: 1, Services: 1, Samples: 1, Range: time.Second}},
		{"zero range", Profile{Series: 1, Services: 1, Samples: 1, Step: time.Second}},
		{"samples mismatch", Profile{Series: 1, Services: 1, Samples: 2, Step: time.Second, Range: time.Second}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.profile.Validate(); err == nil {
				t.Fatal("Validate succeeded")
			}
			if _, err := Generate(tc.profile, 1); err == nil {
				t.Fatal("Generate succeeded")
			}
		})
	}
}
