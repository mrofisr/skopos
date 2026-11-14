// Package app dispatches one Skopos process to the mode selected by
// SKOPOS_MODE.
//
// The command in cmd/skopos only wires the process context and calls Main.
// Every mode lives in its own package under internal/app so that main stays a
// thin composition root.
package app

import (
	"context"
	"fmt"
	"strings"

	checkpointapp "github.com/jawaracloud/skopos/internal/app/checkpoint"
	"github.com/jawaracloud/skopos/internal/app/gateway"
	"github.com/jawaracloud/skopos/internal/app/mcp"
	"github.com/jawaracloud/skopos/internal/appenv"
	"github.com/jawaracloud/skopos/internal/version"
)

// Mode is the role selected by SKOPOS_MODE.
type Mode string

const (
	// ModeAll serves the public API and the three query heads. It is the
	// default when SKOPOS_MODE is unset.
	ModeAll Mode = "all"
	// ModeGateway is an alias for ModeAll.
	ModeGateway Mode = "gateway"
	// ModeQuery serves only the query heads.
	ModeQuery Mode = "query"
	// ModeWorker is a legacy alias for ModeQuery.
	ModeWorker Mode = "worker"
	// ModeFrontend serves health and version locally and proxies queries.
	ModeFrontend Mode = "frontend"
	// ModeMCP serves the MCP tools over stdio or HTTP.
	ModeMCP Mode = "mcp"
	// ModeCheckpoint serves the checkpoint receiver or replays objects.
	ModeCheckpoint Mode = "checkpoint"
)

const usageText = `usage: skopos [version]

SKOPOS_MODE selects the process role:
  all (default), gateway  public API and all query heads
  query, worker          query worker only
  frontend               health/version proxy in front of query workers
  mcp                    MCP server (stdio, or HTTP with SKOPOS_MCP_HTTP_ADDR)
  checkpoint             checkpoint receiver; "skopos replay" replays from S3
`

// ParseMode parses the SKOPOS_MODE value. An empty value means ModeAll.
func ParseMode(raw string) (Mode, error) {
	switch Mode(strings.ToLower(strings.TrimSpace(raw))) {
	case "":
		return ModeAll, nil
	case ModeAll:
		return ModeAll, nil
	case ModeGateway:
		return ModeGateway, nil
	case ModeQuery:
		return ModeQuery, nil
	case ModeWorker:
		return ModeWorker, nil
	case ModeFrontend:
		return ModeFrontend, nil
	case ModeMCP:
		return ModeMCP, nil
	case ModeCheckpoint:
		return ModeCheckpoint, nil
	default:
		return "", fmt.Errorf("SKOPOS_MODE %q is not one of all, gateway, query, worker, frontend, mcp, checkpoint", raw)
	}
}

// ModeFromEnv resolves the mode from the environment.
func ModeFromEnv(env appenv.Env) (Mode, error) {
	return ParseMode(env.Getenv("SKOPOS_MODE"))
}

// Main runs one Skopos invocation and returns the process exit code.
func Main(ctx context.Context, env appenv.Env) int {
	env = env.Resolve()
	if err := Run(ctx, env); err != nil {
		fmt.Fprintf(env.Stderr, "skopos: %v\n", err)
		return 1
	}
	return 0
}

// Run dispatches to the selected mode. A version or help request is handled
// before the mode is loaded, so every mode can report the build version
// without reading mode-specific configuration.
func Run(ctx context.Context, env appenv.Env) error {
	env = env.Resolve()
	if handled, err := handleArgument(env); handled {
		return err
	}
	mode, err := ModeFromEnv(env)
	if err != nil {
		return err
	}
	switch mode {
	case ModeAll, ModeGateway, ModeQuery, ModeWorker, ModeFrontend:
		return gateway.Run(ctx, env)
	case ModeMCP:
		return mcp.Run(ctx, env)
	case ModeCheckpoint:
		return checkpointapp.Run(ctx, env)
	default:
		return fmt.Errorf("SKOPOS_MODE %q is not implemented", mode)
	}
}

func handleArgument(env appenv.Env) (bool, error) {
	if len(env.Args) == 0 {
		return false, nil
	}
	switch env.Args[0] {
	case "version", "-version", "--version":
		_, err := fmt.Fprintln(env.Stdout, version.Version)
		return true, err
	case "help", "-h", "-help", "--help":
		_, err := fmt.Fprint(env.Stdout, usageText)
		return true, err
	default:
		return false, nil
	}
}
