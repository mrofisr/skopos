package changes

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// referenceRE finds path-like literals in Go, shell, and Makefile sources.
// Bare words (Makefile recipes) and quoted strings are both accepted.
var referenceRE = regexp.MustCompile(`(?:^|[\s"'=(])((?:docs|issues|deploy|test)/[A-Za-z0-9_./-]+)`)

type pathReference struct {
	Path   string
	Source string
}

// scanPathReferences walks the repository sources and returns every existing
// file under docs/, issues/, deploy/, or test/ that a source refers to.
func scanPathReferences(root string) ([]pathReference, error) {
	var refs []pathReference
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "vendor", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "cmd/repoctl/internal/changes/") {
			return nil
		}
		if !strings.HasSuffix(rel, ".go") && !strings.HasSuffix(rel, ".sh") && filepath.Base(rel) != "Makefile" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, match := range referenceRE.FindAllStringSubmatch(string(data), -1) {
			reference := normalizeReference(match[1])
			if reference == "" {
				continue
			}
			info, err := os.Stat(filepath.Join(root, reference))
			if err != nil || info.IsDir() {
				continue
			}
			refs = append(refs, pathReference{Path: reference, Source: rel})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Path != refs[j].Path {
			return refs[i].Path < refs[j].Path
		}
		return refs[i].Source < refs[j].Source
	})
	return refs, nil
}

func normalizeReference(raw string) string {
	reference := strings.TrimSpace(raw)
	if index := strings.IndexByte(reference, '#'); index >= 0 {
		reference = reference[:index]
	}
	if index := strings.LastIndexByte(reference, ':'); index >= 0 {
		if _, err := strconv.Atoi(reference[index+1:]); err == nil {
			reference = reference[:index]
		}
	}
	reference = strings.TrimRight(reference, ".,;:)")
	if reference == "" || strings.HasSuffix(reference, "/") || strings.ContainsAny(reference, "*?${}%") {
		return ""
	}
	return reference
}

func TestEveryReferencedPathHasATrigger(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	rules, err := LoadRules(filepath.Join(root, ".github", "ci-paths.json"))
	if err != nil {
		t.Fatal(err)
	}
	refs, err := scanPathReferences(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range refs {
		if len(rules.MatchGroups(ref.Path)) == 0 {
			t.Errorf("%s references %s, which no CI path group matches", ref.Source, ref.Path)
		}
	}
}

func TestCouplingScannerFindsMissingGroup(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "x.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := "package main\n\n// See docs/x.md.\n"
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	config := `{"groups":{"go":["**/*.go"]},"jobs":{"go":["go"]}}`
	path := filepath.Join(root, "ci-paths.json")
	if err := os.WriteFile(path, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	rules, err := LoadRules(path)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := scanPathReferences(root)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ref := range refs {
		if ref.Path == "docs/x.md" && len(rules.MatchGroups(ref.Path)) == 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("scanner did not report docs/x.md as uncovered: %#v", refs)
	}
}

func TestCouplingScannerAcceptsCoveredReference(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "alpha-exit-plan.md"), []byte("plan"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("// See docs/alpha-exit-plan.md.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	config := `{"groups":{"go":["**/*.go","docs/alpha-exit-plan.md"]},"jobs":{"go":["go"]}}`
	path := filepath.Join(root, "ci-paths.json")
	if err := os.WriteFile(path, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	rules, err := LoadRules(path)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := scanPathReferences(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) == 0 {
		t.Fatal("scanner found no references")
	}
	for _, ref := range refs {
		if len(rules.MatchGroups(ref.Path)) == 0 {
			t.Fatalf("%s is not covered", ref.Path)
		}
	}
}
