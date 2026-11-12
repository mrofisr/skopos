package main

import (
	"github.com/jawaracloud/skopos/tools/repoctl/internal/changes"
	"github.com/jawaracloud/skopos/tools/repoctl/internal/cigate"
	"github.com/jawaracloud/skopos/tools/repoctl/internal/codescanning"
	"github.com/jawaracloud/skopos/tools/repoctl/internal/command"
	"github.com/jawaracloud/skopos/tools/repoctl/internal/exitcheck"
	"github.com/jawaracloud/skopos/tools/repoctl/internal/grafanacheck"
	"github.com/jawaracloud/skopos/tools/repoctl/internal/issueops"
	"github.com/jawaracloud/skopos/tools/repoctl/internal/mutation"
	"github.com/jawaracloud/skopos/tools/repoctl/internal/osvexceptions"
	"github.com/jawaracloud/skopos/tools/repoctl/internal/prcheck"
	"github.com/jawaracloud/skopos/tools/repoctl/internal/quality"
	"github.com/jawaracloud/skopos/tools/repoctl/internal/quickstart"
	"github.com/jawaracloud/skopos/tools/repoctl/internal/releaseverify"
	"github.com/jawaracloud/skopos/tools/repoctl/internal/ruleset"
	"github.com/jawaracloud/skopos/tools/repoctl/internal/sbom"
	"github.com/jawaracloud/skopos/tools/repoctl/internal/workflowlint"
	"github.com/jawaracloud/skopos/tools/repoctl/internal/workflowsdoc"
)

// newRegistry is the one place a command is registered. Each subcommand is a
// package under tools/repoctl/internal and adds one line here.
func newRegistry() (*command.Registry, error) {
	return command.NewRegistry(
		changes.Command{},
		cigate.Command{},
		codescanning.Command{},
		exitcheck.Command{},
		grafanacheck.Command{},
		issueops.Command{},
		mutation.Command{},
		osvexceptions.Command{},
		prcheck.Command{},
		quality.Command{},
		releaseverify.Command{},
		ruleset.Command{},
		sbom.Command{},
		quickstart.Command{},
		workflowlint.Command{},
		workflowsdoc.Command{},
	)
}
