package sbom

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrofisr/skopos/tools/repoctl/internal/command"
)

type fakeRunner struct {
	output []byte
	err    error
	calls  [][]string
}

func (f *fakeRunner) Run(context.Context, string, ...string) error { return nil }

func (f *fakeRunner) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	return f.output, f.err
}

func testEnv() (command.Env, *bytes.Buffer) {
	stdout := &bytes.Buffer{}
	return command.Env{
		Stdout: stdout,
		Stderr: &bytes.Buffer{},
		Getenv: func(string) string { return "" },
		Now:    func() time.Time { return time.Date(2026, 11, 12, 10, 30, 0, 0, time.UTC) },
		Run:    &fakeRunner{},
	}, stdout
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunWritesFallbackInventory(t *testing.T) {
	root := t.TempDir()
	chart := filepath.Join(root, "deploy", "helm", "skopos")
	writeFile(t, filepath.Join(chart, "Chart.yaml"), "apiVersion: v2\nname: skopos\n")
	writeFile(t, filepath.Join(chart, "templates", "deploy.yaml"), "kind: Deployment\n")

	output := filepath.Join(root, "out", "skopos.cdx.json")
	env, stdout := testEnv()
	env.Run = &fakeRunner{output: []byte(
		"github.com/example/beta|v1.2.3\n" +
			"github.com/example/alpha|v0.1.0\n" +
			"github.com/example/alpha|v0.1.0\n" +
			"github.com/mrofisr/skopos|\n")}
	cmd := Command{NewUUID: func() (string, error) { return "00000000-0000-4000-8000-000000000000", nil }}
	if err := cmd.Run(context.Background(), env, []string{
		"--image", "skopos:test", "--version", "0.1.0", "--output", output, "--root", root,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var doc document
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decode SBOM: %v", err)
	}
	if doc.BOMFormat != "CycloneDX" || doc.SpecVersion != "1.5" || doc.SerialNumber != "urn:uuid:00000000-0000-4000-8000-000000000000" {
		t.Fatalf("document header = %#v", doc)
	}
	if !doc.Metadata.Timestamp.Equal(time.Date(2026, 11, 12, 10, 30, 0, 0, time.UTC)) {
		t.Fatalf("timestamp = %v", doc.Metadata.Timestamp)
	}
	if doc.Metadata.Component.Name != "skopos:test" || doc.Metadata.Component.Version != "0.1.0" {
		t.Fatalf("component = %#v", doc.Metadata.Component)
	}
	if len(doc.Components) != 4 {
		t.Fatalf("components = %d, want 4: %#v", len(doc.Components), doc.Components)
	}
	if doc.Components[0].Name != "github.com/example/alpha" || doc.Components[0].PURL != "pkg:golang/github.com/example/alpha@v0.1.0" {
		t.Fatalf("first module = %#v", doc.Components[0])
	}
	if doc.Components[0].Licenses[0].License.Name != "NOASSERTION" {
		t.Fatalf("license = %#v", doc.Components[0].Licenses)
	}

	sum := sha256.Sum256([]byte("kind: Deployment\n"))
	wantHash := hex.EncodeToString(sum[:])
	file := componentsByName(doc.Components)["deploy/helm/skopos/templates/deploy.yaml"]
	if file.Name == "" || file.Hashes[0].Content != wantHash {
		t.Fatalf("file component = %#v, want hash %s", file, wantHash)
	}
	if got := stdout.String(); !strings.Contains(got, "4 components") {
		t.Fatalf("stdout = %q", got)
	}
}

func TestRunReadsModulesFromGoList(t *testing.T) {
	root := t.TempDir()
	chart := filepath.Join(root, "chart")
	writeFile(t, filepath.Join(chart, "Chart.yaml"), "name: skopos\n")
	runner := &fakeRunner{output: []byte("example.com/a|v1.2.3\nexample.com/b|v0.4.0\n")}
	env, _ := testEnv()
	env.Run = runner
	output := filepath.Join(root, "sbom.json")

	cmd := Command{NewUUID: func() (string, error) { return "id", nil }}
	if err := cmd.Run(context.Background(), env, []string{
		"--image", "skopos:test", "--version", "dev", "--output", output,
		"--root", root, "--chart", chart,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("calls = %#v, want one go list", runner.calls)
	}
	want := []string{"go", "-C", root, "list", "-deps", "-mod=readonly",
		"-f", "{{with .Module}}{{.Path}}|{{.Version}}{{end}}", "./cmd/skopos"}
	if strings.Join(runner.calls[0], " ") != strings.Join(want, " ") {
		t.Fatalf("go list call = %#v, want %#v", runner.calls[0], want)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "example.com/b") {
		t.Fatalf("SBOM missing go list module:\n%s", data)
	}
}

func TestRunRequiresImageVersionAndOutput(t *testing.T) {
	env, _ := testEnv()
	for _, args := range [][]string{
		{},
		{"--image", "skopos:test"},
		{"--image", "skopos:test", "--version", "dev"},
	} {
		if err := (Command{}).Run(context.Background(), env, args); err == nil {
			t.Fatalf("Run(%v) accepted missing flags", args)
		}
	}
}

func TestRunRequiresGoListRunner(t *testing.T) {
	root := t.TempDir()
	env, _ := testEnv()
	env.Run = nil
	err := (Command{}).Run(context.Background(), env, []string{
		"--image", "skopos:test", "--version", "dev",
		"--output", filepath.Join(root, "sbom.json"), "--root", root,
	})
	if err == nil || !strings.Contains(err.Error(), "command runner") {
		t.Fatalf("Run error = %v, want a command runner error", err)
	}
}

func TestRunRejectsMissingChart(t *testing.T) {
	root := t.TempDir()
	env, _ := testEnv()
	env.Run = &fakeRunner{output: []byte("example.com/a|v1.0.0\n")}
	err := (Command{}).Run(context.Background(), env, []string{
		"--image", "skopos:test", "--version", "dev",
		"--output", filepath.Join(root, "sbom.json"), "--root", root,
	})
	if err == nil || !strings.Contains(err.Error(), "read chart") {
		t.Fatalf("Run error = %v, want a chart error", err)
	}
}

func componentsByName(components []component) map[string]component {
	out := make(map[string]component, len(components))
	for _, c := range components {
		out[c.Name] = c
	}
	return out
}

func TestUUIDIsVersion4(t *testing.T) {
	uuid, err := (Command{}).uuid()
	if err != nil {
		t.Fatal(err)
	}
	if len(uuid) != 36 || uuid[8] != '-' || uuid[13] != '-' || uuid[18] != '-' || uuid[23] != '-' {
		t.Fatalf("uuid = %q", uuid)
	}
	if uuid[14] != '4' || !strings.ContainsRune("89ab", rune(uuid[19])) {
		t.Fatalf("uuid = %q, want a v4 variant", uuid)
	}
}
