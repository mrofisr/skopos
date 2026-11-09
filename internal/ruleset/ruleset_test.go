package ruleset

import (
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

func raw(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal parameter fixture: %v", err)
	}
	return data
}

func validConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		ReviewException: ReviewException{
			RequiredApprovingReviewCount: 0,
			Reason:                       "Solo maintainer: the pull request and required checks are the gate.",
			DocumentedAt:                 "docs/openssf-scorecard.md#review-exception-one-maintainer",
		},
		RequiredWorkflows: []string{"ci.yml", "dependency-review.yml"},
		Ruleset: Ruleset{
			Name:        "main-protection",
			Target:      "branch",
			Enforcement: "active",
			Conditions: Conditions{
				RefName: RefName{Include: []string{"refs/heads/main"}},
			},
			Rules: []Rule{
				{Type: "deletion"},
				{Type: "non_fast_forward"},
				{Type: "required_linear_history"},
				{
					Type: "pull_request",
					Parameters: raw(t, PullRequestParameters{
						RequiredApprovingReviewCount:   0,
						RequiredReviewThreadResolution: true,
						AllowedMergeMethods:            []string{"squash", "rebase"},
					}),
				},
				{
					Type: "required_status_checks",
					Parameters: raw(t, StatusCheckParameters{
						StrictRequiredStatusChecksPolicy: true,
						RequiredStatusChecks: []StatusCheck{
							{Context: "Go build, vet, test"},
							{Context: "Coverage ratchet"},
							{Context: "Dependency review"},
						},
					}),
				},
			},
			BypassActors: []BypassActor{},
		},
	}
}

func validWorkflows() map[string][]byte {
	return map[string][]byte{
		"ci.yml": []byte(`name: CI
on:
  pull_request:
jobs:
  go:
    name: Go build, vet, test
    runs-on: ubuntu-24.04
  coverage:
    name: Coverage ratchet
    runs-on: ubuntu-24.04
`),
		"dependency-review.yml": []byte(`name: Dependency review
on:
  pull_request:
jobs:
  dependency-review:
    name: Dependency review
    runs-on: ubuntu-24.04
`),
	}
}

func setChecks(t *testing.T, cfg *Config, checks ...string) {
	t.Helper()
	required := make([]StatusCheck, 0, len(checks))
	for _, check := range checks {
		required = append(required, StatusCheck{Context: check})
	}
	for index, rule := range cfg.Ruleset.Rules {
		if rule.Type == "required_status_checks" {
			cfg.Ruleset.Rules[index].Parameters = raw(t, StatusCheckParameters{
				StrictRequiredStatusChecksPolicy: true,
				RequiredStatusChecks:             required,
			})
			return
		}
	}
	t.Fatal("fixture has no required_status_checks rule")
}

func setApprovals(t *testing.T, cfg *Config, count int) {
	t.Helper()
	for index, rule := range cfg.Ruleset.Rules {
		if rule.Type == "pull_request" {
			cfg.Ruleset.Rules[index].Parameters = raw(t, PullRequestParameters{
				RequiredApprovingReviewCount:   count,
				RequiredReviewThreadResolution: true,
				AllowedMergeMethods:            []string{"squash", "rebase"},
			})
			return
		}
	}
	t.Fatal("fixture has no pull_request rule")
}

func TestValidateAcceptsConsistentRepository(t *testing.T) {
	cfg := validConfig(t)
	if err := Validate(cfg, validWorkflows()); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	want := []string{"Go build, vet, test", "Coverage ratchet", "Dependency review"}
	if got := cfg.RequiredChecks(); !slices.Equal(got, want) {
		t.Fatalf("RequiredChecks = %v, want %v", got, want)
	}
}

func TestValidateDetectsRenamedJob(t *testing.T) {
	workflows := validWorkflows()
	workflows["ci.yml"] = bytes.ReplaceAll(
		workflows["ci.yml"],
		[]byte("name: Go build, vet, test"),
		[]byte("name: Go test"),
	)
	err := Validate(validConfig(t), workflows)
	if err == nil {
		t.Fatal("Validate = nil, want a renamed-job error")
	}
	for _, want := range []string{
		`required check "Go build, vet, test" is not a job`,
		`workflow job "Go test" in ci.yml is not a required check`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want %q", err, want)
		}
	}
}

func TestValidateDetectsRulesetOnlyCheck(t *testing.T) {
	cfg := validConfig(t)
	setChecks(t, &cfg, "Go build, vet, test", "Coverage ratchet", "Dependency review", "Deploy preview")
	err := Validate(cfg, validWorkflows())
	if err == nil {
		t.Fatal("Validate = nil, want a ruleset-only check error")
	}
	if want := `required check "Deploy preview" is not a job in the required workflows`; !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want %q", err, want)
	}
}

func TestValidateDetectsDuplicateRequiredCheck(t *testing.T) {
	cfg := validConfig(t)
	setChecks(t, &cfg, "Go build, vet, test", "Coverage ratchet", "Dependency review", "Coverage ratchet")
	err := Validate(cfg, validWorkflows())
	if err == nil {
		t.Fatal("Validate = nil, want a duplicate-check error")
	}
	if want := `duplicate required check "Coverage ratchet"`; !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want %q", err, want)
	}
}

func TestValidateRequiresEveryWorkflow(t *testing.T) {
	workflows := validWorkflows()
	delete(workflows, "ci.yml")
	err := Validate(validConfig(t), workflows)
	if err == nil {
		t.Fatal("Validate = nil, want a missing-workflow error")
	}
	if want := `required workflow "ci.yml" is missing`; !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want %q", err, want)
	}
}

func TestValidateRequiresAtLeastOneCheck(t *testing.T) {
	cfg := validConfig(t)
	setChecks(t, &cfg)
	err := Validate(cfg, validWorkflows())
	if err == nil {
		t.Fatal("Validate = nil, want a missing-check error")
	}
	if want := "at least one required status check"; !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want %q", err, want)
	}
}

func TestValidateReviewException(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *Config)
		want   string
	}{
		{
			name: "required approvals are not zero",
			mutate: func(t *testing.T, cfg *Config) {
				setApprovals(t, cfg, 1)
				cfg.ReviewException.RequiredApprovingReviewCount = 1
			},
			want: "pull_request.required_approving_review_count must be 0",
		},
		{
			name: "exception does not match the rule",
			mutate: func(t *testing.T, cfg *Config) {
				setApprovals(t, cfg, 1)
			},
			want: "does not match the pull_request rule",
		},
		{
			name: "reason is missing",
			mutate: func(t *testing.T, cfg *Config) {
				cfg.ReviewException.Reason = ""
			},
			want: "review_exception.reason is required",
		},
		{
			name: "documentation is missing",
			mutate: func(t *testing.T, cfg *Config) {
				cfg.ReviewException.DocumentedAt = ""
			},
			want: "review_exception.documented_at is required",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := validConfig(t)
			test.mutate(t, &cfg)
			err := Validate(cfg, validWorkflows())
			if err == nil {
				t.Fatal("Validate = nil, want an error")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %q, want %q", err, test.want)
			}
		})
	}
}

func TestValidateRulesetShape(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *Config)
		want   string
	}{
		{
			name: "wrong name",
			mutate: func(t *testing.T, cfg *Config) {
				cfg.Ruleset.Name = "main"
			},
			want: `ruleset.name must be "main-protection"`,
		},
		{
			name: "wrong target",
			mutate: func(t *testing.T, cfg *Config) {
				cfg.Ruleset.Target = "tag"
			},
			want: `ruleset.target must be "branch"`,
		},
		{
			name: "inactive",
			mutate: func(t *testing.T, cfg *Config) {
				cfg.Ruleset.Enforcement = "evaluate"
			},
			want: `ruleset.enforcement must be "active"`,
		},
		{
			name: "too many refs",
			mutate: func(t *testing.T, cfg *Config) {
				cfg.Ruleset.Conditions.RefName.Include = []string{"refs/heads/main", "refs/heads/development"}
			},
			want: `ruleset.conditions.ref_name.include must be exactly ["refs/heads/main"]`,
		},
		{
			name: "excluded refs",
			mutate: func(t *testing.T, cfg *Config) {
				cfg.Ruleset.Conditions.RefName.Exclude = []string{"refs/heads/main"}
			},
			want: "ruleset.conditions.ref_name.exclude must be empty",
		},
		{
			name: "missing linear history",
			mutate: func(t *testing.T, cfg *Config) {
				cfg.Ruleset.Rules = slices.DeleteFunc(cfg.Ruleset.Rules, func(rule Rule) bool {
					return rule.Type == "required_linear_history"
				})
			},
			want: `ruleset is missing required rule "required_linear_history"`,
		},
		{
			name: "bypass actor",
			mutate: func(t *testing.T, cfg *Config) {
				cfg.Ruleset.BypassActors = append(cfg.Ruleset.BypassActors, BypassActor{ActorID: 1, ActorType: "User", BypassMode: "always"})
			},
			want: "ruleset.bypass_actors must be empty",
		},
		{
			name: "non-strict checks",
			mutate: func(t *testing.T, cfg *Config) {
				for index, rule := range cfg.Ruleset.Rules {
					if rule.Type == "required_status_checks" {
						cfg.Ruleset.Rules[index].Parameters = raw(t, StatusCheckParameters{
							RequiredStatusChecks: []StatusCheck{
								{Context: "Go build, vet, test"},
								{Context: "Coverage ratchet"},
								{Context: "Dependency review"},
							},
						})
					}
				}
			},
			want: "strict_required_status_checks_policy must be true",
		},
		{
			name: "merge commits allowed",
			mutate: func(t *testing.T, cfg *Config) {
				for index, rule := range cfg.Ruleset.Rules {
					if rule.Type == "pull_request" {
						cfg.Ruleset.Rules[index].Parameters = raw(t, PullRequestParameters{
							RequiredReviewThreadResolution: true,
							AllowedMergeMethods:            []string{"merge", "squash"},
						})
					}
				}
			},
			want: `pull_request.allowed_merge_methods must not include "merge"`,
		},
		{
			name: "unknown rule",
			mutate: func(t *testing.T, cfg *Config) {
				cfg.Ruleset.Rules = append(cfg.Ruleset.Rules, Rule{Type: "creation"})
			},
			want: `unknown ruleset rule "creation"`,
		},
		{
			name: "missing pull request parameters",
			mutate: func(t *testing.T, cfg *Config) {
				for index, rule := range cfg.Ruleset.Rules {
					if rule.Type == "pull_request" {
						cfg.Ruleset.Rules[index].Parameters = nil
					}
				}
			},
			want: "pull_request.parameters is required",
		},
		{
			name: "missing status check parameters",
			mutate: func(t *testing.T, cfg *Config) {
				for index, rule := range cfg.Ruleset.Rules {
					if rule.Type == "required_status_checks" {
						cfg.Ruleset.Rules[index].Parameters = nil
					}
				}
			},
			want: "required_status_checks.parameters is required",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := validConfig(t)
			test.mutate(t, &cfg)
			err := Validate(cfg, validWorkflows())
			if err == nil {
				t.Fatal("Validate = nil, want an error")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %q, want %q", err, test.want)
			}
		})
	}
}

func TestWorkflowJobNames(t *testing.T) {
	tests := []struct {
		name     string
		workflow string
		want     []string
		wantErr  string
	}{
		{
			name: "named and unnamed jobs",
			workflow: `name: CI
jobs:
  build:
    name: Build
    runs-on: ubuntu-24.04
  test:
    runs-on: ubuntu-24.04
`,
			want: []string{"Build", "test"},
		},
		{
			name: "quoted names",
			workflow: `jobs:
  double:
    name: "Double quoted"
  single:
    name: 'Single quoted'
`,
			want: []string{"Double quoted", "Single quoted"},
		},
		{
			name: "stops at the next top-level key",
			workflow: `jobs:
  build:
    name: Build
permissions:
  contents: read
`,
			want: []string{"Build"},
		},
		{
			name:     "requires a jobs key",
			workflow: "name: CI\non: push\n",
			wantErr:  "jobs",
		},
		{
			name:     "rejects tabs",
			workflow: "jobs:\n\tbuild:\n    name: Build\n",
			wantErr:  "tab",
		},
		{
			name:     "rejects a scalar in the jobs block",
			workflow: "jobs:\n  build\n",
			wantErr:  "job",
		},
		{
			name:     "rejects an empty name",
			workflow: "jobs:\n  build:\n    name:\n",
			wantErr:  "name",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := WorkflowJobNames([]byte(test.workflow))
			if test.wantErr != "" {
				if err == nil {
					t.Fatalf("WorkflowJobNames = %v, want %q", got, test.wantErr)
				}
				if !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("error = %q, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("WorkflowJobNames: %v", err)
			}
			if !slices.Equal(got, test.want) {
				t.Fatalf("WorkflowJobNames = %v, want %v", got, test.want)
			}
		})
	}
}

func TestValidateReportsWorkflowParseErrors(t *testing.T) {
	workflows := validWorkflows()
	workflows["ci.yml"] = []byte("jobs:\n\tbuild:\n    name: Build\n")
	err := Validate(validConfig(t), workflows)
	if err == nil {
		t.Fatal("Validate = nil, want a workflow parse error")
	}
	for _, want := range []string{"parse ci.yml", "tab"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want %q", err, want)
		}
	}
}

func TestParseRejectsUnknownField(t *testing.T) {
	if _, err := Parse([]byte(`{"unexpected": true}`)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("Parse error = %v, want unknown field", err)
	}
}

func TestParseRejectsTrailingData(t *testing.T) {
	if _, err := Parse([]byte("{}\n{}")); err == nil || !strings.Contains(err.Error(), "trailing") {
		t.Fatalf("Parse error = %v, want trailing data", err)
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load("testdata/absent.json"); err == nil {
		t.Fatal("Load = nil, want an error")
	}
}

func TestLoadRealRuleset(t *testing.T) {
	cfg, err := Load("../../docs/security/ruleset.json")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	workflows := make(map[string][]byte, len(cfg.RequiredWorkflows))
	for _, name := range cfg.RequiredWorkflows {
		data, err := os.ReadFile("../../.github/workflows/" + name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		workflows[name] = data
	}
	if err := Validate(cfg, workflows); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	want := []string{
		"Go build, vet, test",
		"Coverage ratchet",
		"ClickHouse integration",
		"Tempo and Loki differentials",
		"cerberus three-way differential",
		"S3 tiered storage and backup (RustFS)",
		"Helm lint and render",
		"Workflow policy",
		"Dependency review",
	}
	if got := cfg.RequiredChecks(); !slices.Equal(got, want) {
		t.Fatalf("RequiredChecks = %v, want %v", got, want)
	}
}
