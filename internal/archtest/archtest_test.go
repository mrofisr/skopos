package archtest

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const (
	module       = "github.com/jawaracloud/skopos"
	baselinePath = "test/quality/layout-violations.txt"
	maxMainLines = 50
)

const (
	ruleCmdBinary       = "cmd-binary"
	ruleInternalTooling = "internal-tooling"
	ruleRepoctlImport   = "repoctl-import"
	rulePackageName     = "package-name"
	rulePython          = "python"
	ruleMainSize        = "main-size"
	ruleShellLocation   = "shell-location"
)

var allowedCmdBinaries = map[string]bool{
	"skopos": true,
}

var forbiddenInternalTooling = map[string]bool{
	"codescanning":  true,
	"grafanacheck":  true,
	"issueops":      true,
	"osvexceptions": true,
	"prcheck":       true,
	"quality":       true,
	"quickstart":    true,
	"releaseverify": true,
	"ruleset":       true,
}

var forbiddenPackageNames = map[string]bool{
	"util":    true,
	"common":  true,
	"helpers": true,
	"misc":    true,
	"types":   true,
}

// allowedProductImports is the one exception list for rule 11 of AGENTS.md.
// It starts empty. Adding an entry needs a reason in the PR and a test update.
var allowedProductImports = map[string]bool{}

// allowedShellScripts are deployment assets that must stay beside the files
// that package or install them. Every other shell script lives under scripts/.
var allowedShellScripts = map[string]bool{
	"deploy/helm/skopos/files/migrate.sh": true,
}

type pkg struct {
	ImportPath string
	Name       string
	Dir        string
	Imports    []string
}

type violation struct {
	Rule string
	Path string
}

func (v violation) key() string { return v.Rule + "\t" + v.Path }

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	return root
}

func listPackages(t *testing.T, root string) []pkg {
	t.Helper()
	cmd := exec.Command("go", "list", "-json", "./...")
	cmd.Dir = root
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go list ./...: %v\n%s", err, stderr.String())
	}
	dec := json.NewDecoder(&stdout)
	var packages []pkg
	for {
		var p pkg
		err := dec.Decode(&p)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("decode go list output: %v", err)
		}
		packages = append(packages, p)
	}
	return packages
}

func relative(importPath string) string {
	return strings.TrimPrefix(importPath, module+"/")
}

func cmdBinaryViolations(root string) ([]violation, error) {
	entries, err := os.ReadDir(filepath.Join(root, "cmd"))
	if err != nil {
		return nil, fmt.Errorf("read cmd/: %w", err)
	}
	var out []violation
	for _, entry := range entries {
		if !entry.IsDir() || allowedCmdBinaries[entry.Name()] {
			continue
		}
		out = append(out, violation{Rule: ruleCmdBinary, Path: "cmd/" + entry.Name()})
	}
	return out, nil
}

func internalToolingViolations(packages []pkg) []violation {
	var out []violation
	for _, p := range packages {
		rel := relative(p.ImportPath)
		if !strings.HasPrefix(rel, "internal/") {
			continue
		}
		if forbiddenInternalTooling[path.Base(rel)] {
			out = append(out, violation{Rule: ruleInternalTooling, Path: rel})
		}
	}
	return out
}

func repoctlImportViolations(packages []pkg) []violation {
	var out []violation
	for _, p := range packages {
		rel := relative(p.ImportPath)
		if rel != "tools/repoctl" && !strings.HasPrefix(rel, "tools/repoctl/") {
			continue
		}
		for _, imported := range p.Imports {
			if !strings.HasPrefix(imported, module+"/internal/") {
				continue
			}
			importedRel := relative(imported)
			if allowedProductImports[importedRel] {
				continue
			}
			out = append(out, violation{
				Rule: ruleRepoctlImport,
				Path: rel + " imports " + importedRel,
			})
		}
	}
	return out
}

func packageNameViolations(packages []pkg) []violation {
	var out []violation
	for _, p := range packages {
		rel := relative(p.ImportPath)
		if rel == "" {
			continue
		}
		if forbiddenPackageNames[p.Name] || forbiddenPackageNames[path.Base(rel)] {
			out = append(out, violation{Rule: rulePackageName, Path: rel})
		}
	}
	return out
}

func pythonViolations(root string, skip ...string) ([]violation, error) {
	skipped := make(map[string]bool, len(skip))
	for _, dir := range skip {
		skipped[filepath.ToSlash(dir)] = true
	}
	var out []violation
	err := filepath.WalkDir(root, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			name := entry.Name()
			if name == ".git" || name == "vendor" || name == "node_modules" {
				return filepath.SkipDir
			}
			rel, relErr := filepath.Rel(root, current)
			if relErr != nil {
				return relErr
			}
			if skipped[filepath.ToSlash(rel)] {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.EqualFold(filepath.Ext(entry.Name()), ".py") {
			rel, relErr := filepath.Rel(root, current)
			if relErr != nil {
				return relErr
			}
			out = append(out, violation{Rule: rulePython, Path: filepath.ToSlash(rel)})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func shellViolations(root string, skip ...string) ([]violation, error) {
	skipped := make(map[string]bool, len(skip))
	for _, dir := range skip {
		skipped[filepath.ToSlash(dir)] = true
	}
	var out []violation
	err := filepath.WalkDir(root, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			name := entry.Name()
			if name == ".git" || name == "vendor" || name == "node_modules" || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			rel, relErr := filepath.Rel(root, current)
			if relErr != nil {
				return relErr
			}
			if skipped[filepath.ToSlash(rel)] {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(entry.Name()) != ".sh" {
			return nil
		}
		rel, relErr := filepath.Rel(root, current)
		if relErr != nil {
			return relErr
		}
		slash := filepath.ToSlash(rel)
		if strings.HasPrefix(slash, "scripts/") || allowedShellScripts[slash] {
			return nil
		}
		out = append(out, violation{Rule: ruleShellLocation, Path: slash})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func mainSizeViolations(root string) ([]violation, error) {
	entries, err := os.ReadDir(filepath.Join(root, "cmd"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []violation
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		rel := path.Join("cmd", entry.Name(), "main.go")
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		lines := strings.Count(strings.TrimRight(string(data), "\n"), "\n") + 1
		if lines > maxMainLines {
			out = append(out, violation{
				Rule: ruleMainSize,
				Path: fmt.Sprintf("%s (%d lines, max %d)", rel, lines, maxMainLines),
			})
		}
	}
	return out, nil
}

func loadBaseline(t *testing.T, file string) map[string]violation {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	out := make(map[string]violation)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		rule, p, ok := strings.Cut(line, "\t")
		if !ok || strings.TrimSpace(rule) == "" || strings.TrimSpace(p) == "" {
			t.Fatalf("%s:%d: expected rule<TAB>path, got %q", file, lineNo, line)
		}
		v := violation{Rule: strings.TrimSpace(rule), Path: strings.TrimSpace(p)}
		if _, duplicate := out[v.key()]; duplicate {
			t.Fatalf("%s:%d: duplicate violation %q", file, lineNo, v.key())
		}
		out[v.key()] = v
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	return out
}

// diffBaseline returns one message per new or stale violation for rule.
func diffBaseline(rule string, computed []violation, baseline map[string]violation) []string {
	seen := make(map[string]bool, len(computed))
	var problems []string
	for _, v := range computed {
		key := v.key()
		if seen[key] {
			problems = append(problems, "duplicate computed violation "+key)
			continue
		}
		seen[key] = true
		if _, ok := baseline[key]; !ok {
			problems = append(problems, "new violation "+key+"; fix it, or add it to "+baselinePath+" with a reason")
		}
	}
	for key, v := range baseline {
		if v.Rule != rule {
			continue
		}
		if !seen[key] {
			problems = append(problems, "stale violation "+key+"; remove it from "+baselinePath)
		}
	}
	sort.Strings(problems)
	return problems
}

func assertBaseline(t *testing.T, rule string, computed []violation, baseline map[string]violation) {
	t.Helper()
	for _, problem := range diffBaseline(rule, computed, baseline) {
		t.Error(problem)
	}
}

func TestCmdContainsOnlyAllowedBinaries(t *testing.T) {
	root := repoRoot(t)
	computed, err := cmdBinaryViolations(root)
	if err != nil {
		t.Fatal(err)
	}
	assertBaseline(t, ruleCmdBinary, computed, loadBaseline(t, filepath.Join(root, baselinePath)))
}

func TestNoRepoToolingInInternal(t *testing.T) {
	root := repoRoot(t)
	assertBaseline(t, ruleInternalTooling, internalToolingViolations(listPackages(t, root)), loadBaseline(t, filepath.Join(root, baselinePath)))
}

func TestRepoctlImportsOnlyAllowedProductPackages(t *testing.T) {
	root := repoRoot(t)
	assertBaseline(t, ruleRepoctlImport, repoctlImportViolations(listPackages(t, root)), loadBaseline(t, filepath.Join(root, baselinePath)))
}

func TestNoUtilPackages(t *testing.T) {
	root := repoRoot(t)
	assertBaseline(t, rulePackageName, packageNameViolations(listPackages(t, root)), loadBaseline(t, filepath.Join(root, baselinePath)))
}

func TestNoPythonFiles(t *testing.T) {
	root := repoRoot(t)
	computed, err := pythonViolations(root, "internal/archtest/testdata")
	if err != nil {
		t.Fatal(err)
	}
	assertBaseline(t, rulePython, computed, loadBaseline(t, filepath.Join(root, baselinePath)))
}

func TestShellScriptsAreCentralized(t *testing.T) {
	root := repoRoot(t)
	computed, err := shellViolations(root, "internal/archtest/testdata")
	if err != nil {
		t.Fatal(err)
	}
	assertBaseline(t, ruleShellLocation, computed, loadBaseline(t, filepath.Join(root, baselinePath)))
}

func TestMainIsSmall(t *testing.T) {
	root := repoRoot(t)
	computed, err := mainSizeViolations(root)
	if err != nil {
		t.Fatal(err)
	}
	assertBaseline(t, ruleMainSize, computed, loadBaseline(t, filepath.Join(root, baselinePath)))
}

func TestRulesFireOnFixtures(t *testing.T) {
	root := filepath.Join("testdata", "tree")

	cmdViolations, err := cmdBinaryViolations(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(cmdViolations) != 1 || cmdViolations[0].Path != "cmd/skopos-tool" {
		t.Fatalf("cmd violations = %#v, want cmd/skopos-tool", cmdViolations)
	}

	pyRoot := t.TempDir()
	pyFile := filepath.Join(pyRoot, "test", "bench", "bench.py")
	if err := os.MkdirAll(filepath.Dir(pyFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pyFile, []byte("print('fixture')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pyViolations, err := pythonViolations(pyRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(pyViolations) != 1 || pyViolations[0].Path != "test/bench/bench.py" {
		t.Fatalf("python violations = %#v, want test/bench/bench.py", pyViolations)
	}

	shellViolations, err := shellViolations(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(shellViolations) != 1 || shellViolations[0].Path != "test/scripts/run.sh" {
		t.Fatalf("shell violations = %#v, want test/scripts/run.sh", shellViolations)
	}

	mainViolations, err := mainSizeViolations(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(mainViolations) != 1 || !strings.Contains(mainViolations[0].Path, "cmd/skopos/main.go") {
		t.Fatalf("main-size violations = %#v, want cmd/skopos/main.go", mainViolations)
	}

	packages := []pkg{
		{ImportPath: module + "/internal/prcheck", Name: "prcheck"},
		{ImportPath: module + "/internal/util", Name: "util"},
		{ImportPath: module + "/internal/product", Name: "product"},
		{ImportPath: module + "/tools/repoctl", Name: "main", Imports: []string{
			module + "/internal/config",
			module + "/tools/repoctl/internal/command",
		}},
		{ImportPath: module + "/tools/repoctl/internal/quality", Name: "quality", Imports: []string{
			module + "/tools/repoctl/internal/command",
		}},
	}
	if got := internalToolingViolations(packages); len(got) != 1 || got[0].Path != "internal/prcheck" {
		t.Fatalf("internal-tooling violations = %#v, want internal/prcheck", got)
	}
	if got := packageNameViolations(packages); len(got) != 1 || got[0].Path != "internal/util" {
		t.Fatalf("package-name violations = %#v, want internal/util", got)
	}
	if got := repoctlImportViolations(packages); len(got) != 1 || !strings.Contains(got[0].Path, "internal/config") {
		t.Fatalf("repoctl-import violations = %#v, want internal/config", got)
	}
}

func TestBaselineRatchet(t *testing.T) {
	existing := violation{Rule: ruleCmdBinary, Path: "cmd/skopos-old"}
	tests := []struct {
		name     string
		computed []violation
		baseline map[string]violation
		want     string
	}{
		{
			name:     "new violation fails",
			computed: []violation{{Rule: ruleCmdBinary, Path: "cmd/skopos-new"}},
			baseline: map[string]violation{},
			want:     "new violation",
		},
		{
			name:     "stale violation fails",
			computed: nil,
			baseline: map[string]violation{existing.key(): existing},
			want:     "stale violation",
		},
		{
			name:     "listed violation passes",
			computed: []violation{existing},
			baseline: map[string]violation{existing.key(): existing},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			problems := diffBaseline(ruleCmdBinary, test.computed, test.baseline)
			if test.want == "" {
				if len(problems) != 0 {
					t.Fatalf("problems = %v, want none", problems)
				}
				return
			}
			if len(problems) != 1 || !strings.Contains(problems[0], test.want) {
				t.Fatalf("problems = %v, want one containing %q", problems, test.want)
			}
		})
	}
}
