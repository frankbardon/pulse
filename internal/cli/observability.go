package cli

import (
	"context"
	"io"
	"log/slog"
	"strings"

	"github.com/frankbardon/pulse"
	perrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/obsprom"
	cli "github.com/urfave/cli/v3"
)

// Log flag names and their accepted values. The flags are root
// persistent flags (urfave/cli v3 flags are persistent unless Local),
// so every leaf accepts them in any position.
const (
	flagLogLevel  = "log-level"
	flagLogFormat = "log-format"

	logLevelOff = "off"

	logFormatText = "text"
	logFormatJSON = "json"
)

var logLevels = map[string]slog.Level{
	"debug": slog.LevelDebug,
	"info":  slog.LevelInfo,
	"warn":  slog.LevelWarn,
	"error": slog.LevelError,
}

// LogFlags returns the root persistent --log-level / --log-format flags.
// Logs never reach stdout: `--json` output and the `pulse mcp` JSON-RPC
// transport both depend on a clean stdout.
func LogFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:  flagLogLevel,
			Usage: "Structured log level written to stderr (off|debug|info|warn|error); off logs nothing",
			Value: logLevelOff,
		},
		&cli.StringFlag{
			Name:  flagLogFormat,
			Usage: "Structured log format on stderr (text|json); ignored while --log-level is off",
			Value: logFormatText,
		},
	}
}

// loggerKey carries the root-built *slog.Logger to the leaves.
type loggerKey struct{}

// LogBefore is the root command's Before: it builds the stderr logger
// the --log-level / --log-format flags ask for (nil for off) and hands
// it to every leaf through ctx, where newPulse / newPulseOpts thread it
// into pulse.Options.Logger. An unknown level or format is CLI_INPUT.
func LogBefore(ctx context.Context, cmd *cli.Command) (context.Context, error) {
	lg, err := newLogger(cmd.String(flagLogLevel), cmd.String(flagLogFormat), cmd.Root().ErrWriter)
	if err != nil {
		return ctx, err
	}
	if lg == nil {
		return ctx, nil
	}
	return context.WithValue(ctx, loggerKey{}, lg), nil
}

// loggerFrom returns the logger LogBefore installed, nil when logging
// is off (pulse.Options.Logger nil = silent).
func loggerFrom(ctx context.Context) *slog.Logger {
	if ctx == nil {
		return nil
	}
	lg, _ := ctx.Value(loggerKey{}).(*slog.Logger)
	return lg
}

// newLogger builds the slog logger for level and format writing to w.
// Level "off" (or empty) returns nil.
func newLogger(level, format string, w io.Writer) (*slog.Logger, error) {
	level = strings.ToLower(strings.TrimSpace(level))
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		format = logFormatText
	}
	if format != logFormatText && format != logFormatJSON {
		return nil, logFlagError(flagLogFormat, format, "--log-format "+format+": want text or json")
	}
	if level == "" || level == logLevelOff {
		return nil, nil
	}
	lv, ok := logLevels[level]
	if !ok {
		return nil, logFlagError(flagLogLevel, level, "--log-level "+level+": want off, debug, info, warn or error")
	}
	opts := &slog.HandlerOptions{Level: lv}
	if format == logFormatJSON {
		return slog.New(slog.NewJSONHandler(w, opts)), nil
	}
	return slog.New(slog.NewTextHandler(w, opts)), nil
}

func logFlagError(flag, value, msg string) error {
	return perrors.NewCodedErrorWithDetails(perrors.CLI_INPUT, msg,
		map[string]any{"flag": flag, "value": value})
}

// startMetrics serves the `pulse mcp --metrics-addr` exporter: a fresh
// obsprom registry bound to addr. Empty addr starts nothing and returns
// nils — no port is ever opened unless asked. A bad or busy address is
// CLI_INPUT.
func startMetrics(addr string, reg *obsprom.Registry) (*obsprom.Server, error) {
	if addr == "" || reg == nil {
		return nil, nil
	}
	srv, err := obsprom.Listen(addr, reg)
	if err != nil {
		return nil, perrors.NewCodedErrorWithDetails(perrors.CLI_INPUT,
			"--metrics-addr "+addr+": "+err.Error(),
			map[string]any{"flag": "metrics-addr", "value": addr})
	}
	return srv, nil
}

// withLogger sets o.Logger from ctx unless the caller already set one.
func withLogger(ctx context.Context, o pulse.Options) pulse.Options {
	if o.Logger == nil {
		o.Logger = loggerFrom(ctx)
	}
	return o
}

// activeLogLevel is level as the startup notice names it: empty when
// logging is off.
func activeLogLevel(level string) string {
	level = strings.ToLower(strings.TrimSpace(level))
	if level == logLevelOff {
		return ""
	}
	return level
}
