// Package quality enforces the alpha-exit coverage ratchet.
package quality

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
)

// DefaultModule is the module path used when a floor file omits it.
const DefaultModule = "github.com/jawaracloud/skopos"

// Coverage is the measured statement coverage for one package.
type Coverage struct {
	Package    string
	Statements int
	Covered    int
	Percent    float64
}

// Floor is the checked-in minimum coverage per package.
type Floor struct {
	Module   string             `json:"module"`
	Packages map[string]float64 `json:"packages"`
}

type accumulator struct {
	statements int
	covered    int
}

// ParseProfile reads a Go coverage profile and computes exact per-package
// statement coverage.
func ParseProfile(r io.Reader) (map[string]Coverage, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	blocks := map[string]*accumulator{}
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "mode: ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return nil, fmt.Errorf("coverage profile line %d: expected 3 fields, got %d", lineNo, len(fields))
		}
		colon := strings.LastIndex(fields[0], ":")
		if colon <= 0 || !strings.Contains(fields[0][colon+1:], ",") {
			return nil, fmt.Errorf("coverage profile line %d: malformed block %q", lineNo, fields[0])
		}
		statements, err := strconv.Atoi(fields[1])
		if err != nil || statements < 0 {
			return nil, fmt.Errorf("coverage profile line %d: invalid statement count %q", lineNo, fields[1])
		}
		count, err := strconv.Atoi(fields[2])
		if err != nil || count < 0 {
			return nil, fmt.Errorf("coverage profile line %d: invalid hit count %q", lineNo, fields[2])
		}
		pkg := path.Dir(fields[0][:colon])
		block := blocks[pkg]
		if block == nil {
			block = &accumulator{}
			blocks[pkg] = block
		}
		block.statements += statements
		if count > 0 {
			block.covered += statements
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(blocks) == 0 {
		return nil, fmt.Errorf("coverage profile has no blocks")
	}
	out := make(map[string]Coverage, len(blocks))
	for pkg, block := range blocks {
		percent := 0.0
		if block.statements > 0 {
			percent = 100 * float64(block.covered) / float64(block.statements)
		}
		out[pkg] = Coverage{
			Package:    pkg,
			Statements: block.statements,
			Covered:    block.covered,
			Percent:    percent,
		}
	}
	return out, nil
}

// LoadFloor reads a floor file.
func LoadFloor(r io.Reader) (*Floor, error) {
	var floor Floor
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&floor); err != nil {
		return nil, err
	}
	if floor.Module == "" {
		floor.Module = DefaultModule
	}
	if floor.Packages == nil {
		floor.Packages = map[string]float64{}
	}
	for pkg, value := range floor.Packages {
		if value < 0 || value > 100 {
			return nil, fmt.Errorf("floor %s = %v, want 0..100", pkg, value)
		}
	}
	return &floor, nil
}

// LoadFloorFile reads a floor file from disk.
func LoadFloorFile(path string) (*Floor, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return LoadFloor(f)
}

// RenderFloor builds the floor file from measured coverage. Only gate
// packages are recorded; test-only packages are not part of the product.
func RenderFloor(cov map[string]Coverage, module string) *Floor {
	if module == "" {
		module = DefaultModule
	}
	floor := &Floor{Module: module, Packages: map[string]float64{}}
	for pkg, c := range cov {
		rel := relativePackage(pkg, module)
		if rel == "" || !isGatePackage(rel) {
			continue
		}
		floor.Packages[rel] = math.Floor(c.Percent*10) / 10
	}
	return floor
}

// Write renders a floor file as stable JSON.
func (f *Floor) Write(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(f)
}

// Gate compares measured coverage with the floor and with the previous floor.
// It returns every problem, not only the first one.
func (f *Floor) Gate(cov map[string]Coverage, previous *Floor) []error {
	var errs []error
	module := f.Module
	if module == "" {
		module = DefaultModule
	}
	measured := map[string]Coverage{}
	for _, c := range GatePackages(cov, module) {
		measured[c.Package] = c
	}
	for rel, c := range measured {
		floor, ok := f.Packages[rel]
		if !ok {
			errs = append(errs, fmt.Errorf("%s: measured %.1f%% but the package is missing from the floor file", rel, c.Percent))
			continue
		}
		if c.Percent+1e-9 < floor {
			errs = append(errs, fmt.Errorf("%s: coverage %.1f%% is below the floor %.1f%%", rel, c.Percent, floor))
		}
	}
	for rel := range f.Packages {
		if _, ok := measured[rel]; !ok {
			errs = append(errs, fmt.Errorf("%s: has a floor but is missing from the coverage profile", rel))
		}
	}
	if previous != nil {
		for rel, oldFloor := range previous.Packages {
			newFloor, ok := f.Packages[rel]
			if !ok {
				errs = append(errs, fmt.Errorf("%s: floor was removed (was %.1f%%)", rel, oldFloor))
				continue
			}
			if newFloor+1e-9 < oldFloor {
				errs = append(errs, fmt.Errorf("%s: floor lowered from %.1f%% to %.1f%%", rel, oldFloor, newFloor))
			}
		}
	}
	sort.Slice(errs, func(i, j int) bool { return errs[i].Error() < errs[j].Error() })
	return errs
}

// Sorted returns the measured packages in name order.
func Sorted(cov map[string]Coverage) []Coverage {
	out := make([]Coverage, 0, len(cov))
	for _, c := range cov {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Package < out[j].Package })
	return out
}

// GatePackages returns the measured product packages in name order, with
// package names relative to the module.
func GatePackages(cov map[string]Coverage, module string) []Coverage {
	if module == "" {
		module = DefaultModule
	}
	out := make([]Coverage, 0, len(cov))
	for pkg, c := range cov {
		rel := relativePackage(pkg, module)
		if rel == "" || !isGatePackage(rel) {
			continue
		}
		c.Package = rel
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Package < out[j].Package })
	return out
}

func relativePackage(pkg, module string) string {
	prefix := module + "/"
	if strings.HasPrefix(pkg, prefix) {
		return strings.TrimPrefix(pkg, prefix)
	}
	return ""
}

func isGatePackage(rel string) bool {
	return strings.HasPrefix(rel, "internal/") || strings.HasPrefix(rel, "cmd/")
}
