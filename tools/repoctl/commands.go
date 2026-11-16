package main

import (
	"github.com/mrofisr/skopos/tools/repoctl/internal/benchgate"
	"github.com/mrofisr/skopos/tools/repoctl/internal/changes"
	"github.com/mrofisr/skopos/tools/repoctl/internal/cigate"
	"github.com/mrofisr/skopos/tools/repoctl/internal/codescanning"
	"github.com/mrofisr/skopos/tools/repoctl/internal/command"
	"github.com/mrofisr/skopos/tools/repoctl/internal/evidence"
	"github.com/mrofisr/skopos/tools/repoctl/internal/exitcheck"
	"github.com/mrofisr/skopos/tools/repoctl/internal/grafanacheck"
	"github.com/mrofisr/skopos/tools/repoctl/internal/issueops"
	"github.com/mrofisr/skopos/tools/repoctl/internal/mutation"
	"github.com/mrofisr/skopos/tools/repoctl/internal/osvexceptions"
	"github.com/mrofisr/skopos/tools/repoctl/internal/prcheck"
	"github.com/mrofisr/skopos/tools/repoctl/internal/quality"
	"github.com/mrofisr/skopos/tools/repoctl/internal/quickstart"
	"github.com/mrofisr/skopos/tools/repoctl/internal/releaseverify"
	"github.com/mrofisr/skopos/tools/repoctl/internal/ruleset"
	"github.com/mrofisr/skopos/tools/repoctl/internal/sbom"
	"github.com/mrofisr/skopos/tools/repoctl/internal/workflowlint"
	"github.com/mrofisr/skopos/tools/repoctl/internal/workflowsdoc"
)

// newRegistry is the one place a command is registered. Each subcommand is a
// package under tools/repoctl/internal and adds one line here.
func newRegistry() (*command.Registry, error) {
	return command.NewRegistry(
		benchgate.Command{},
		changes.Command{},
		cigate.Command{},
		codescanning.Command{},
		evidence.Command{},
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
