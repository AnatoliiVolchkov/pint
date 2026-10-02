package main

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"

	"github.com/urfave/cli/v3"
)

const metricsqlModule = "github.com/VictoriaMetrics/metricsql"

var versionCmd = &cli.Command{
	Name:   "version",
	Usage:  "Print version and exit.",
	Action: actionVersion,
}

func actionVersion(_ context.Context, _ *cli.Command) error {
	fmt.Printf("%s (revision: %s)\n", version, commit)
	fmt.Printf("metricsql:  %s\n", metricsqlVersion(debug.ReadBuildInfo()))
	return nil
}

// metricsqlVersion returns the version of the MetricsQL parser compiled into pint.
// When the module is replaced, for example by a fork, it returns the version of the
// replacement and the version it replaces.
func metricsqlVersion(info *debug.BuildInfo, ok bool) string {
	if !ok || info == nil {
		return "unknown"
	}
	for _, dep := range info.Deps {
		if dep.Path != metricsqlModule {
			continue
		}
		if dep.Replace == nil {
			return dep.Version
		}
		replacement := dep.Replace.Version
		if replacement == "" {
			// Replaced with a local directory.
			replacement = dep.Replace.Path
		}
		return fmt.Sprintf("%s (replaces %s %s)", replacement, strings.TrimPrefix(metricsqlModule, "github.com/"), dep.Version)
	}
	return "unknown"
}
