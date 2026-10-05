package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/cloudflare/pint/internal/config"
)

const (
	configFlag   = "config"
	logLevelFlag = "log-level"
	enabledFlag  = "enabled"
	disabledFlag = "disabled"
	offlineFlag  = "offline"
	noColorFlag  = "no-color"
	workersFlag  = "workers"
	showDupsFlag = "show-duplicates"

	externalLabelFlag    = "external-label"
	externalLabelsEnvVar = "PINT_EXTERNAL_LABELS"
)

var (
	version = "unknown"
	commit  = "unknown"
)

func newApp() *cli.Command {
	return &cli.Command{
		Usage: "Prometheus rule linter/validator.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    configFlag,
				Aliases: []string{"c"},
				Value:   ".pint.hcl",
				Usage:   "Configuration file to use.",
			},
			&cli.IntFlag{
				Name:    workersFlag,
				Aliases: []string{"w"},
				Value:   10,
				Usage:   "Number of worker threads for running checks.",
			},
			&cli.StringFlag{
				Name:    logLevelFlag,
				Aliases: []string{"l"},
				Value:   slog.LevelInfo.String(),
				Usage:   "Log level.",
			},
			&cli.BoolFlag{
				Name:    noColorFlag,
				Aliases: []string{"n"},
				Value:   false,
				Usage:   "Disable output colouring.",
				Sources: cli.EnvVars("NO_COLOR"),
			},
			&cli.StringSliceFlag{
				Name:    disabledFlag,
				Aliases: []string{"d"},
				Usage:   "List of checks to disable (example: promql/cost).",
			},
			&cli.StringSliceFlag{
				Name:    enabledFlag,
				Aliases: []string{"e"},
				Usage:   "Only enable these checks (example: promql/cost).",
			},
			&cli.BoolFlag{
				Name:    offlineFlag,
				Aliases: []string{"o"},
				Value:   false,
				Usage:   "Disable all checks that send live queries to Prometheus servers.",
			},
			&cli.StringSliceFlag{
				Name: externalLabelFlag,
				Usage: "External labels set on vmalert with -external.label, as name=value pairs " +
					"separated by commas or by repeating the flag (example: account=alpha,dc=test). " +
					"Added to externalLabels of all VictoriaMetrics servers, overriding labels with the same name.",
				Sources: cli.EnvVars(externalLabelsEnvVar),
			},
			&cli.BoolFlag{
				Name:    showDupsFlag,
				Aliases: []string{"s"},
				Value:   false,
				Usage:   "Show all reported problems including the same issue duplicated across multiple rules.",
			},
		},
		Commands: []*cli.Command{
			versionCmd,
			lintCmd,
			ciCmd,
			watchCmd,
			configCmd,
			parseCmd,
		},
	}
}

type actionMeta struct {
	cfg       config.Config
	isOffline bool
	workers   int
}

func actionSetup(c *cli.Command) (meta actionMeta, err error) {
	err = initLogger(c.String(logLevelFlag), c.Bool(noColorFlag))
	if err != nil {
		return meta, fmt.Errorf("failed to set log level: %w", err)
	}

	meta.workers = c.Int(workersFlag)
	if meta.workers < 1 {
		return meta, fmt.Errorf("--%s flag must be > 0", workersFlag)
	}

	var fromFile bool
	meta.cfg, fromFile, err = config.Load(c.String(configFlag), c.IsSet(configFlag))
	if err != nil {
		return meta, fmt.Errorf("failed to load config file %q: %w", c.String(configFlag), err)
	}
	if fromFile {
		slog.LogAttrs(context.Background(), slog.LevelDebug, "Adding pint config to the parser exclude list", slog.String("path", c.String(configFlag)))
		meta.cfg.Parser.Exclude = append(meta.cfg.Parser.Exclude, c.String(configFlag))
	}

	if err = applyExternalLabels(c, &meta.cfg); err != nil {
		return meta, err
	}

	meta.cfg.SetDisabledChecks(c.StringSlice(disabledFlag))
	enabled := c.StringSlice(enabledFlag)
	if len(enabled) > 0 {
		meta.cfg.Checks.Enabled = enabled
	}

	if c.Bool(offlineFlag) {
		meta.isOffline = true
		meta.cfg.DisableOnlineChecks()
	}

	return meta, nil
}

// applyExternalLabels adds labels from the --external-label flag or the
// PINT_EXTERNAL_LABELS environment variable to all VictoriaMetrics servers.
func applyExternalLabels(c *cli.Command, cfg *config.Config) error {
	labels, err := config.ParseExternalLabels(c.StringSlice(externalLabelFlag))
	if err != nil {
		return fmt.Errorf("invalid --%s flag or %s environment variable: %w", externalLabelFlag, externalLabelsEnvVar, err)
	}
	if len(labels) == 0 {
		return nil
	}
	updated := cfg.SetExternalLabels(labels)
	if updated == 0 {
		slog.LogAttrs(
			context.Background(), slog.LevelWarn,
			"External labels are only used by VictoriaMetrics servers, no prometheus server uses the victoriametrics backend",
			slog.Any("labels", labels),
		)
		return nil
	}
	slog.LogAttrs(
		context.Background(), slog.LevelInfo,
		"Using external labels from the command line",
		slog.Any("labels", labels),
		slog.Int("servers", updated),
	)
	return nil
}

func main() {
	ctx := context.Background()
	app := newApp()
	err := app.Run(ctx, os.Args)
	if err != nil {
		slog.LogAttrs(ctx, slog.LevelError, "Execution completed with error(s)", slog.Any("err", err))
		os.Exit(1)
	}
}
