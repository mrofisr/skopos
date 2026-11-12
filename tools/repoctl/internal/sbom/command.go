// Package sbom generates the documented CycloneDX fallback inventory.
//
// It is used by scripts/release/release.sh when syft is not installed. The
// inventory is deliberately conservative: it records the Go module graph and
// the SHA-256 hash of every chart source file, and marks package licenses as
// NOASSERTION.
package sbom

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jawaracloud/skopos/tools/repoctl/internal/command"
)

// Command implements `repoctl sbom`.
type Command struct {
	// NewUUID returns the document serial number. It is injectable so tests can
	// produce a stable document. A nil value uses a random UUID v4.
	NewUUID func() (string, error)
}

// Name implements command.Command.
func (Command) Name() string { return "sbom" }

// Summary implements command.Command.
func (Command) Summary() string {
	return "write the CycloneDX fallback inventory (Go modules and chart files)"
}

type options struct {
	image   string
	version string
	output  string
	root    string
	chart   string
}

type document struct {
	BOMFormat    string      `json:"bomFormat"`
	SpecVersion  string      `json:"specVersion"`
	SerialNumber string      `json:"serialNumber"`
	Version      int         `json:"version"`
	Metadata     metadata    `json:"metadata"`
	Components   []component `json:"components"`
}

type metadata struct {
	Timestamp  time.Time  `json:"timestamp"`
	Tools      []tool     `json:"tools"`
	Component  component  `json:"component"`
	Properties []property `json:"properties"`
}

type tool struct {
	Vendor  string `json:"vendor"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

type property struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type component struct {
	Type     string    `json:"type"`
	BOMRef   string    `json:"bom-ref"`
	Name     string    `json:"name"`
	Version  string    `json:"version,omitempty"`
	PURL     string    `json:"purl,omitempty"`
	Licenses []license `json:"licenses,omitempty"`
	Hashes   []hash    `json:"hashes,omitempty"`
}

type license struct {
	License licenseName `json:"license"`
}

type licenseName struct {
	Name string `json:"name"`
}

type hash struct {
	Alg     string `json:"alg"`
	Content string `json:"content"`
}

// Run implements command.Command.
func (c Command) Run(ctx context.Context, env command.Env, args []string) error {
	fs := flag.NewFlagSet("sbom", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	o := options{root: "."}
	fs.StringVar(&o.image, "image", "", "container image name")
	fs.StringVar(&o.version, "version", "", "container image version")
	fs.StringVar(&o.output, "output", "", "CycloneDX output path")
	fs.StringVar(&o.root, "root", ".", "repository root")
	fs.StringVar(&o.chart, "chart", "", "chart directory (default <root>/deploy/helm/skopos)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments %v", fs.Args())
	}
	if o.image == "" || o.version == "" || o.output == "" {
		return fmt.Errorf("--image, --version, and --output are required")
	}
	if o.chart == "" {
		o.chart = filepath.Join(o.root, "deploy", "helm", "skopos")
	}

	modules, err := readModules(ctx, env, o.root)
	if err != nil {
		return err
	}
	components, err := moduleComponents(modules)
	if err != nil {
		return err
	}
	fileComponents, err := chartComponents(o.chart)
	if err != nil {
		return err
	}
	components = append(components, fileComponents...)

	uuid, err := c.uuid()
	if err != nil {
		return err
	}
	doc := document{
		BOMFormat:    "CycloneDX",
		SpecVersion:  "1.5",
		SerialNumber: "urn:uuid:" + uuid,
		Version:      1,
		Metadata: metadata{
			Timestamp: env.Now().UTC().Truncate(time.Second),
			Tools:     []tool{{Vendor: "Skopos", Name: "repoctl sbom", Version: "1"}},
			Component: component{
				Type:    "container",
				BOMRef:  "container:" + o.image,
				Name:    o.image,
				Version: o.version,
			},
			Properties: []property{
				{Name: "skopos:sbom:generator", Value: "go-module-inventory-fallback"},
				{Name: "skopos:sbom:note", Value: "Install syft for a full image SBOM."},
			},
		},
		Components: components,
	}

	var buf strings.Builder
	encoder := json.NewEncoder(&buf)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(doc); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(o.output), 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	if err := os.WriteFile(o.output, []byte(buf.String()), 0o644); err != nil {
		return fmt.Errorf("write SBOM: %w", err)
	}
	fmt.Fprintf(env.Stdout, "sbom: wrote %s (%d components)\n", o.output, len(components))
	return nil
}

func (c Command) uuid() (string, error) {
	if c.NewUUID != nil {
		return c.NewUUID()
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate serial number: %w", err)
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16]), nil
}

type module struct {
	path    string
	version string
}

func readModules(ctx context.Context, env command.Env, root string) ([]module, error) {
	if env.Run == nil {
		return nil, fmt.Errorf("go list requires a command runner")
	}
	out, err := env.Run.Output(ctx, "go", "-C", root, "list", "-deps", "-mod=readonly",
		"-f", "{{with .Module}}{{.Path}}|{{.Version}}{{end}}", "./cmd/skopos")
	if err != nil {
		return nil, err
	}
	return parseModules(out), nil
}

func parseModules(data []byte) []module {
	var modules []module
	seen := make(map[string]bool)
	for _, line := range strings.Split(string(data), "\n") {
		path, version, ok := strings.Cut(strings.TrimSpace(line), "|")
		if !ok {
			continue
		}
		path, version = strings.TrimSpace(path), strings.TrimSpace(version)
		if path == "" || version == "" {
			continue
		}
		key := path + "|" + version
		if seen[key] {
			continue
		}
		seen[key] = true
		modules = append(modules, module{path: path, version: version})
	}
	sort.Slice(modules, func(i, j int) bool {
		if modules[i].path != modules[j].path {
			return modules[i].path < modules[j].path
		}
		return modules[i].version < modules[j].version
	})
	return modules
}

func moduleComponents(modules []module) ([]component, error) {
	out := make([]component, 0, len(modules))
	for _, m := range modules {
		ref := fmt.Sprintf("pkg:golang/%s@%s", m.path, m.version)
		out = append(out, component{
			Type:     "library",
			BOMRef:   ref,
			Name:     m.path,
			Version:  m.version,
			PURL:     ref,
			Licenses: []license{{License: licenseName{Name: "NOASSERTION"}}},
		})
	}
	return out, nil
}

func chartComponents(chart string) ([]component, error) {
	var out []component
	err := filepath.WalkDir(chart, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(chart, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		digest, err := sha256File(path)
		if err != nil {
			return err
		}
		name := "deploy/helm/skopos/" + rel
		out = append(out, component{
			Type:   "file",
			BOMRef: "chart:" + name,
			Name:   name,
			Hashes: []hash{{Alg: "SHA-256", Content: digest}},
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read chart %s: %w", chart, err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BOMRef < out[j].BOMRef })
	return out, nil
}

func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}
