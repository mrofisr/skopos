package quickstart

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestExtractTaggedBlocks(t *testing.T) {
	markdown := `# README

` + "```sh" + `
not a quickstart block
` + "```" + `

` + "```sh quickstart" + `
helm install skopos oci://registry.example/charts/skopos \
  --version 0.1.0-alpha.2 \
  -f values.release.yaml
` + "```" + `

` + "~~~bash quickstart" + `
helm upgrade skopos oci://registry.example/charts/skopos --version=0.1.0-alpha.2
` + "~~~" + `
`
	blocks, err := Extract([]byte(markdown))
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("blocks = %d, want 2", len(blocks))
	}
	if !strings.Contains(blocks[0].Command, "helm install") || !strings.Contains(blocks[0].Command, "--version 0.1.0-alpha.2") {
		t.Fatalf("first command = %q", blocks[0].Command)
	}
	if !strings.Contains(blocks[1].Command, "helm upgrade") {
		t.Fatalf("second command = %q", blocks[1].Command)
	}
	if blocks[0].Line == 0 || blocks[0].Line >= blocks[1].Line {
		t.Fatalf("line numbers = %d, %d", blocks[0].Line, blocks[1].Line)
	}
}

func TestExtractRejectsNonShellBlock(t *testing.T) {
	markdown := "```text quickstart\nhello\n```\n"
	_, err := Extract([]byte(markdown))
	if err == nil || !strings.Contains(err.Error(), "shell block") {
		t.Fatalf("Extract error = %v, want a shell-block error", err)
	}
}

func TestExtractRejectsUnclosedBlock(t *testing.T) {
	markdown := "```sh quickstart\nhelm install skopos oci://registry.example/charts/skopos --version 0.1.0-alpha.2\n"
	_, err := Extract([]byte(markdown))
	if err == nil || !strings.Contains(err.Error(), "unclosed") {
		t.Fatalf("Extract error = %v, want an unclosed-fence error", err)
	}
}

func TestExtractRequiresQuickstartTag(t *testing.T) {
	markdown := "```sh\nhelm install skopos oci://registry.example/charts/skopos --version 0.1.0-alpha.2\n```\n"
	_, err := Extract([]byte(markdown))
	if err == nil || !strings.Contains(err.Error(), "no quickstart") {
		t.Fatalf("Extract error = %v, want a missing-tag error", err)
	}
}

func TestExtractRejectsEmptyQuickstartBlock(t *testing.T) {
	markdown := "```sh quickstart\n\n```\n"
	_, err := Extract([]byte(markdown))
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("Extract error = %v, want an empty-block error", err)
	}
}

func TestExtractRejectsUnclosedUntaggedFence(t *testing.T) {
	markdown := "```sh\nhelm install skopos oci://registry.example/charts/skopos --version 0.1.0-alpha.2\n"
	_, err := Extract([]byte(markdown))
	if err == nil || !strings.Contains(err.Error(), "unclosed fence") {
		t.Fatalf("Extract error = %v, want an unclosed-fence error", err)
	}
}

func TestBuildPlan(t *testing.T) {
	blocks := []Block{{
		Command: "helm install skopos oci://registry.example/charts/skopos \\\n  --version 0.1.0-alpha.2 \\\n  -f values.release.yaml",
		Line:    1,
	}}
	plan, err := Build(blocks)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if plan.ChartRef != "oci://registry.example/charts/skopos" {
		t.Fatalf("ChartRef = %q", plan.ChartRef)
	}
	if plan.Version != "0.1.0-alpha.2" {
		t.Fatalf("Version = %q", plan.Version)
	}
	if len(plan.Commands) != 1 || plan.Commands[0] != blocks[0].Command {
		t.Fatalf("Commands = %q", plan.Commands)
	}
}

func TestBuildRejectsInvalidPlans(t *testing.T) {
	tests := []struct {
		name   string
		blocks []Block
		want   string
	}{
		{
			name: "no blocks",
			want: "no quickstart",
		},
		{
			name:   "not an install",
			blocks: []Block{{Command: "helm status skopos oci://registry.example/charts/skopos --version 0.1.0-alpha.2", Line: 1}},
			want:   "helm install or helm upgrade",
		},
		{
			name:   "no chart reference",
			blocks: []Block{{Command: "helm install skopos --version 0.1.0-alpha.2", Line: 1}},
			want:   "exactly one oci:// chart",
		},
		{
			name: "two chart references",
			blocks: []Block{{
				Command: "helm install one oci://registry.example/charts/skopos --version 0.1.0-alpha.2\nhelm install two oci://registry.example/charts/other --version 0.1.0-alpha.2",
				Line:    1,
			}},
			want: "exactly one oci:// chart",
		},
		{
			name:   "no version",
			blocks: []Block{{Command: "helm install skopos oci://registry.example/charts/skopos", Line: 1}},
			want:   "exactly one --version",
		},
		{
			name:   "v prefix",
			blocks: []Block{{Command: "helm install skopos oci://registry.example/charts/skopos --version v0.1.0-alpha.2", Line: 1}},
			want:   "invalid chart version",
		},
		{
			name:   "latest",
			blocks: []Block{{Command: "helm install skopos oci://registry.example/charts/skopos --version latest", Line: 1}},
			want:   "invalid chart version",
		},
		{
			name:   "empty version",
			blocks: []Block{{Command: "helm install skopos oci://registry.example/charts/skopos --version=", Line: 1}},
			want:   "invalid chart version",
		},
		{
			name: "multiple versions",
			blocks: []Block{{
				Command: "helm install skopos oci://registry.example/charts/skopos --version 0.1.0-alpha.2\nhelm upgrade skopos oci://registry.example/charts/skopos --version 0.1.0-alpha.3",
				Line:    1,
			}},
			want: "multiple chart versions",
		},
		{
			name:   "invalid chart reference",
			blocks: []Block{{Command: "helm install skopos oci://registry.example --version 0.1.0-alpha.2", Line: 1}},
			want:   "invalid chart reference",
		},
		{
			name:   "chart reference with query",
			blocks: []Block{{Command: "helm install skopos oci://registry.example/charts/skopos?tag=latest --version 0.1.0-alpha.2", Line: 1}},
			want:   "invalid chart reference",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Build(test.blocks)
			if err == nil {
				t.Fatal("Build = nil, want an error")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %q, want %q", err, test.want)
			}
		})
	}
}

func TestRealREADMEQuickstart(t *testing.T) {
	data, err := os.ReadFile("../../../../README.md")
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	blocks, err := Extract(data)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	plan, err := Build(blocks)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if plan.ChartRef != "oci://ghcr.io/jawaracloud/charts/skopos" {
		t.Fatalf("ChartRef = %q", plan.ChartRef)
	}
	if plan.Version != "0.1.0-alpha.3" {
		t.Fatalf("Version = %q", plan.Version)
	}
	if len(plan.Commands) != 1 {
		t.Fatalf("Commands = %d, want 1", len(plan.Commands))
	}
}

func TestPlanCommandsAreCopied(t *testing.T) {
	blocks := []Block{{Command: "helm install skopos oci://registry.example/charts/skopos --version 0.1.0-alpha.2", Line: 1}}
	plan, err := Build(blocks)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	blocks[0].Command = "changed"
	if plan.Commands[0] != "helm install skopos oci://registry.example/charts/skopos --version 0.1.0-alpha.2" {
		t.Fatalf("Commands aliases the input: %q", plan.Commands[0])
	}
	if !slices.Equal(plan.Commands, []string{"helm install skopos oci://registry.example/charts/skopos --version 0.1.0-alpha.2"}) {
		t.Fatalf("Commands = %q", plan.Commands)
	}
}

func TestBuildAcceptsQuotedVersionAndReference(t *testing.T) {
	blocks := []Block{{
		Command: "helm install skopos \"oci://registry.example/charts/skopos\" --version=\"0.1.0-alpha.2\"",
		Line:    1,
	}}
	plan, err := Build(blocks)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if plan.ChartRef != "oci://registry.example/charts/skopos" || plan.Version != "0.1.0-alpha.2" {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestExtractSkipsDeeplyIndentedFences(t *testing.T) {
	body := "    ```sh quickstart\n    helm install skopos oci://ghcr.io/jawaracloud/charts/skopos --version 0.1.0\n    ```\n"
	if _, err := Extract([]byte(body)); err == nil || !strings.Contains(err.Error(), "no quickstart-tagged") {
		t.Fatalf("error = %v, want the indented fence to be skipped", err)
	}
}

func TestValidateChartRefRejectsMalformedURL(t *testing.T) {
	if err := validateChartRef("oci://ghcr.io/%zz"); err == nil || !strings.Contains(err.Error(), "invalid chart reference") {
		t.Fatalf("error = %v, want an invalid-reference error", err)
	}
}

func TestFenceEndRejectsDeeplyIndentedCloser(t *testing.T) {
	if fenceEnd("    ```", '`', 3) {
		t.Fatal("fenceEnd accepted a closer indented by four spaces")
	}
}
