package main

import (
	"github.com/jawaracloud/skopos/cmd/repoctl/internal/changes"
	"github.com/jawaracloud/skopos/cmd/repoctl/internal/cigate"
	"github.com/jawaracloud/skopos/cmd/repoctl/internal/codescanning"
	"github.com/jawaracloud/skopos/cmd/repoctl/internal/command"
	"github.com/jawaracloud/skopos/cmd/repoctl/internal/exitcheck"
	"github.com/jawaracloud/skopos/cmd/repoctl/internal/grafanacheck"
	"github.com/jawaracloud/skopos/cmd/repoctl/internal/issueops"
	"github.com/jawaracloud/skopos/cmd/repoctl/internal/mutation"
	"github.com/jawaracloud/skopos/cmd/repoctl/internal/osvexceptions"
	"github.com/jawaracloud/skopos/cmd/repoctl/internal/prcheck"
	"github.com/jawaracloud/skopos/cmd/repoctl/internal/quality"
	"github.com/jawaracloud/skopos/cmd/repoctl/internal/quickstart"
	"github.com/jawaracloud/skopos/cmd/repoctl/internal/releaseverify"
	"github.com/jawaracloud/skopos/cmd/repoctl/internal/ruleset"
	"github.com/jawaracloud/skopos/cmd/repoctl/internal/workflowlint"
	"github.com/jawaracloud/skopos/cmd/repoctl/internal/workflowsdoc"
)

// newRegistry is the one place a command is registered. Each subcommand is a
// package under cmd/repoctl/internal and adds one line here.
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
		quickstart.Command{},
		workflowlint.Command{},
		workflowsdoc.Command{},
	)
}
