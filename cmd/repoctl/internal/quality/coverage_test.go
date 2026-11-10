package quality

import (
	"strings"
	"testing"
)

const fixtureProfile = `mode: set
github.com/jawaracloud/skopos/internal/auth/auth.go:1.1,2.2 1 1
github.com/jawaracloud/skopos/internal/sqlgen/emit.go:1.1,2.2 2 1
github.com/jawaracloud/skopos/internal/sqlgen/emit.go:3.1,4.2 2 0
github.com/jawaracloud/skopos/test/compat/seed/seed.go:1.1,2.2 10 1
`

func TestParseProfile_ComputesExactPercent(t *testing.T) {
	cov, err := ParseProfile(strings.NewReader(fixtureProfile))
	if err != nil {
		t.Fatalf("ParseProfile: %v", err)
	}
	auth := cov["github.com/jawaracloud/skopos/internal/auth"]
	if auth.Percent != 100 || auth.Statements != 1 || auth.Covered != 1 {
		t.Errorf("auth = %#v", auth)
	}
	sqlgen := cov["github.com/jawaracloud/skopos/internal/sqlgen"]
	if sqlgen.Percent != 50 || sqlgen.Statements != 4 || sqlgen.Covered != 2 {
		t.Errorf("sqlgen = %#v", sqlgen)
	}
}

func TestParseProfile_RejectsMalformedLine(t *testing.T) {
	_, err := ParseProfile(strings.NewReader("mode: set\nnot a profile line\n"))
	if err == nil {
		t.Fatal("ParseProfile accepted a malformed line")
	}
}

func TestCoverageGate_FailsBelowFloor(t *testing.T) {
	cov, err := ParseProfile(strings.NewReader(fixtureProfile))
	if err != nil {
		t.Fatal(err)
	}
	floor := &Floor{Module: "github.com/jawaracloud/skopos", Packages: map[string]float64{
		"internal/auth":   100,
		"internal/sqlgen": 60,
	}}
	errs := floor.Gate(cov, nil)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "internal/sqlgen") {
		t.Fatalf("errors = %v, want one sqlgen floor error", errs)
	}
}

func TestCoverageGate_MissingPackageFails(t *testing.T) {
	cov, err := ParseProfile(strings.NewReader(fixtureProfile))
	if err != nil {
		t.Fatal(err)
	}
	floor := &Floor{Module: "github.com/jawaracloud/skopos", Packages: map[string]float64{
		"internal/auth": 100,
	}}
	errs := floor.Gate(cov, nil)
	if len(errs) == 0 || !strings.Contains(strings.Join(errorStrings(errs), " "), "missing") {
		t.Fatalf("errors = %v, want a missing package error", errs)
	}
}

func TestCoverageGate_MissingPackageFromProfileFails(t *testing.T) {
	cov, err := ParseProfile(strings.NewReader(fixtureProfile))
	if err != nil {
		t.Fatal(err)
	}
	floor := &Floor{Module: "github.com/jawaracloud/skopos", Packages: map[string]float64{
		"internal/auth":    100,
		"internal/sqlgen":  50,
		"internal/removed": 10,
	}}
	errs := floor.Gate(cov, nil)
	if !strings.Contains(strings.Join(errorStrings(errs), " "), "internal/removed") {
		t.Fatalf("errors = %v, want the removed package named", errs)
	}
}

func TestCoverageGate_LoweredFloorFails(t *testing.T) {
	cov, err := ParseProfile(strings.NewReader(fixtureProfile))
	if err != nil {
		t.Fatal(err)
	}
	floor := &Floor{Module: "github.com/jawaracloud/skopos", Packages: map[string]float64{
		"internal/auth":   100,
		"internal/sqlgen": 50,
	}}
	previous := &Floor{Module: floor.Module, Packages: map[string]float64{
		"internal/auth":   100,
		"internal/sqlgen": 60,
	}}
	errs := floor.Gate(cov, previous)
	if !strings.Contains(strings.Join(errorStrings(errs), " "), "lowered") {
		t.Fatalf("errors = %v, want a lowered floor error", errs)
	}
}

func TestCoverageGate_PassesAtFloor(t *testing.T) {
	cov, err := ParseProfile(strings.NewReader(fixtureProfile))
	if err != nil {
		t.Fatal(err)
	}
	floor := &Floor{Module: "github.com/jawaracloud/skopos", Packages: map[string]float64{
		"internal/auth":   100,
		"internal/sqlgen": 50,
	}}
	if errs := floor.Gate(cov, nil); len(errs) > 0 {
		t.Fatalf("errors = %v, want none", errs)
	}
}

func TestRenderFloor_RoundsDownAndSkipsTestPackages(t *testing.T) {
	cov, err := ParseProfile(strings.NewReader(fixtureProfile))
	if err != nil {
		t.Fatal(err)
	}
	floor := RenderFloor(cov, "github.com/jawaracloud/skopos")
	if got := floor.Packages["internal/sqlgen"]; got != 50 {
		t.Errorf("sqlgen floor = %v, want 50", got)
	}
	if _, ok := floor.Packages["test/compat/seed"]; ok {
		t.Errorf("test package was included in the floor")
	}
}

func errorStrings(errs []error) []string {
	out := make([]string, 0, len(errs))
	for _, err := range errs {
		out = append(out, err.Error())
	}
	return out
}
