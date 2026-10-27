package cli

import (
	stderrors "errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/frankbardon/pulse"
	perrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/mcpserve"
	cli "github.com/urfave/cli/v3"
)

// TestLimitFlag_IsRootPersistent: --limit is one repeatable string
// slice on the root, and no leaf redeclares it (a local twin would
// shadow the persistent flag).
func TestLimitFlag_IsRootPersistent(t *testing.T) {
	t.Parallel()

	flags := LimitFlags()
	if len(flags) != 1 || !slices.Contains(flags[0].Names(), "limit") {
		t.Fatalf("LimitFlags() = %v, want the one --limit flag", flags)
	}
	if _, ok := flags[0].(*cli.StringSliceFlag); !ok {
		t.Fatalf("--limit is %T, want *cli.StringSliceFlag", flags[0])
	}
	for _, leaf := range []*cli.Command{MCPCommand("test"), RecommendCommand()} {
		for _, f := range leaf.Flags {
			if slices.Contains(f.Names(), "limit") {
				t.Errorf("`pulse %s` redeclares --limit locally", leaf.Name)
			}
		}
	}
}

// TestLimitFlags_ThreadIntoOptions: the root --limit layer reaches
// pulse.Options through withGlobals — the path newPulseOpts, the docs
// and import leaves and `pulse mcp` all take; a caller's own explicit
// key wins; unset flags leave Options.Limits zero.
//
// Falsified by dropping withLimits from withGlobals.
func TestLimitFlags_ThreadIntoOptions(t *testing.T) {
	t.Parallel()

	ctx, err := runWorkersBefore(t, "--limit", "max_compose_slots=2000", "--limit", "request_timeout=5s")
	if err != nil {
		t.Fatal(err)
	}
	o := withGlobals(ctx, pulse.Options{})
	if o.Limits.MaxComposeSlots != 2000 || o.Limits.RequestTimeout != 5*time.Second || o.Limits.MaxGroups != 0 {
		t.Errorf("withGlobals: Limits = %+v", o.Limits)
	}
	if mo := mcpPulseOptions(ctx, t.TempDir()); mo.Limits != o.Limits {
		t.Errorf("pulse mcp options: Limits = %+v, want %+v", mo.Limits, o.Limits)
	}
	own := withGlobals(ctx, pulse.Options{Limits: pulse.Limits{MaxComposeSlots: 7}})
	if own.Limits.MaxComposeSlots != 7 || own.Limits.RequestTimeout != 5*time.Second {
		t.Errorf("caller key not kept / flag key not filled: %+v", own.Limits)
	}
	unset, err := runWorkersBefore(t)
	if err != nil {
		t.Fatal(err)
	}
	if o := withGlobals(unset, pulse.Options{}); o.Limits != (pulse.Limits{}) {
		t.Errorf("unset flags: Limits = %+v, want zero", o.Limits)
	}
}

// TestLimitFlags_BadIsUsageError: a bad --limit is refused before any
// leaf runs — ExitUsage, carrying CLI_INPUT {flag, value, limit?} — the
// rule a negative worker count follows.
//
// Falsified by returning err unwrapped (no usageExit) from limitsBefore.
func TestLimitFlags_BadIsUsageError(t *testing.T) {
	t.Parallel()

	for spec, limit := range map[string]string{"max_widgets=5": "", "max_groups=lots": "max_groups"} {
		_, err := runWorkersBefore(t, "--limit", spec)
		if status, _ := ExitStatus(err); status != ExitUsage {
			t.Fatalf("--limit %s: exit status %d (err %v), want %d", spec, status, err, ExitUsage)
		}
		var ce *perrors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != perrors.CLI_INPUT || ce.Details["flag"] != "limit" || ce.Details["value"] != spec {
			t.Fatalf("--limit %s: err = %#v", spec, err)
		}
		if got, _ := ce.Details["limit"].(string); got != limit {
			t.Errorf("--limit %s: details.limit = %q, want %q", spec, got, limit)
		}
	}
}

// TestMCPPulseOptions_CarriesLimits: the --limit layer reaches the mcp
// instance — Options.Limits wins over a profile per key, and an unset
// key still resolves to the default.
func TestMCPPulseOptions_CarriesLimits(t *testing.T) {
	// serial: t.Setenv mutates the process environment

	t.Setenv("PULSE_FEATURE_PROFILE", "")
	ctx, err := runWorkersBefore(t, "--limit", "max_groups=42", "--limit", "request_timeout=5s")
	if err != nil {
		t.Fatal(err)
	}
	p, err := mcpserve.NewPulse(mcpPulseOptions(ctx, t.TempDir()), mcpserve.Options{})
	if err != nil {
		t.Fatalf("NewPulse: %v", err)
	}
	got := p.Limits()
	if got.MaxGroups != 42 || got.RequestTimeout != 5*time.Second || got.MaxMatrixDim != limits.DefaultMaxMatrixDim {
		t.Fatalf("effective limits = %+v", got)
	}
}

// TestParseLimitFlags_Good: every accepted spelling lands on its field;
// unset names stay 0 (default / profile layer); a later flag wins.
func TestParseLimitFlags_Good(t *testing.T) {
	t.Parallel()

	got, err := parseLimitFlags([]string{
		"max_groups=1000000",
		"request_timeout=30s",
		"max_matrix_dim=unlimited",
		"max_compose_slots=-1",
		"max_chain_stages=10_000",
		"max_crosstab_cells=0",
		"max_join_build_rows=5",
		"max_join_build_rows=6",
	})
	if err != nil {
		t.Fatalf("parseLimitFlags: %v", err)
	}
	want := limits.Limits{
		MaxGroups:        1_000_000,
		RequestTimeout:   30 * time.Second,
		MaxMatrixDim:     limits.Unlimited,
		MaxComposeSlots:  limits.Unlimited,
		MaxChainStages:   10_000,
		MaxJoinBuildRows: 6,
	}
	if got != want {
		t.Fatalf("parseLimitFlags = %+v\nwant %+v", got, want)
	}
	if got, err := parseLimitFlags(nil); err != nil || got != (limits.Limits{}) {
		t.Fatalf("no flags = %+v, %v; want the zero input layer", got, err)
	}
	if got, _ := parseLimitFlags([]string{"request_timeout=unlimited"}); got.RequestTimeout != limits.Unlimited {
		t.Errorf("request_timeout=unlimited = %v", got.RequestTimeout)
	}
}

// TestParseLimitFlags_Refused: an unknown name, a malformed pair or a
// bad value is CLI_INPUT naming the flag.
func TestParseLimitFlags_Refused(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"max_widgets=5":            "",
		"MaxGroups=5":              "",
		"max_groups":               "",
		"=5":                       "",
		"max_groups=lots":          "max_groups",
		"max_groups=-2":            "max_groups",
		"max_groups=":              "max_groups",
		"max_groups=30s":           "max_groups",
		"request_timeout=30":       "request_timeout",
		"request_timeout=-5s":      "request_timeout",
		"max_estimated_memory=1e9": "max_estimated_memory",
	}
	for spec, limit := range cases {
		_, err := parseLimitFlags([]string{"max_groups=1", spec})
		var ce *perrors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != perrors.CLI_INPUT {
			t.Errorf("%q: err = %v, want CLI_INPUT", spec, err)
			continue
		}
		if ce.Details["flag"] != "limit" || ce.Details["value"] != spec {
			t.Errorf("%q: details = %v", spec, ce.Details)
		}
		if got, _ := ce.Details["limit"].(string); got != limit {
			t.Errorf("%q: details.limit = %q, want %q", spec, got, limit)
		}
		if limit == "" && !strings.Contains(ce.Message, "max_join_build_rows") && !strings.Contains(ce.Message, "name=value") {
			t.Errorf("%q: message does not list the known names or the grammar: %s", spec, ce.Message)
		}
	}
}
