package cli

import (
	stderrors "errors"
	"slices"
	"strings"
	"testing"
	"time"

	perrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/mcpserve"
	cli "github.com/urfave/cli/v3"
)

// TestMCPCommand_HasTheCohortScanKnob. A flag that is not mounted cannot be
// passed: unmounted, `pulse mcp` always walks the data directory at startup,
// which is the cost the knob exists to remove. The env source matters as much
// as the flag — an MCP client config usually sets `env`, not `args`, so a
// flag-only knob is unreachable from the place operators configure the server.
func TestMCPCommand_HasTheCohortScanKnob(t *testing.T) {
	cmd := MCPCommand("test")

	var flag cli.Flag
	for _, f := range cmd.Flags {
		if slices.Contains(f.Names(), "no-cohort-scan") {
			flag = f
		}
	}
	if flag == nil {
		t.Fatalf("`pulse mcp` has no --no-cohort-scan flag: %v", flagNames(cmd))
	}

	boolFlag, ok := flag.(*cli.BoolFlag)
	if !ok {
		t.Fatalf("--no-cohort-scan is %T, want *cli.BoolFlag", flag)
	}
	// Default off: an existing invocation keeps enumerating cohorts.
	if boolFlag.Value {
		t.Error("--no-cohort-scan defaults to true; the scan must stay on unless asked otherwise")
	}
	if !slices.Contains(envVarNames(boolFlag), "PULSE_MCP_NO_COHORT_SCAN") {
		t.Errorf("--no-cohort-scan is not reachable through PULSE_MCP_NO_COHORT_SCAN: sources %v", boolFlag.Sources.EnvKeys())
	}
}

func envVarNames(f *cli.BoolFlag) []string {
	return f.Sources.EnvKeys()
}

func flagNames(cmd *cli.Command) []string {
	var out []string
	for _, f := range cmd.Flags {
		out = append(out, f.Names()...)
	}
	return out
}

// TestMCPStartupLine_ReportsTheEffectiveSettings pins the stderr notice to
// mcpserve.Describe's effective values: a profile that turns the cohort
// scan off must read "cohort-scan: false", and a loaded profile is named.
func TestMCPStartupLine_ReportsTheEffectiveSettings(t *testing.T) {
	cases := []struct {
		info mcpserve.ServeInfo
		want string
	}{
		{mcpserve.ServeInfo{CohortScan: true},
			"pulse mcp: serving over stdio (data dir: /d, bind-on-open: true, cohort-scan: true)"},
		{mcpserve.ServeInfo{CohortScan: false, FeatureProfileLoaded: true, FeatureProfile: "self-serve"},
			"pulse mcp: serving over stdio (data dir: /d, bind-on-open: true, cohort-scan: false, feature-profile: self-serve)"},
		{mcpserve.ServeInfo{CohortScan: true, FeatureProfileLoaded: true},
			"pulse mcp: serving over stdio (data dir: /d, bind-on-open: true, cohort-scan: true, feature-profile: (unnamed))"},
	}
	for _, tc := range cases {
		if got := mcpStartupLine("/d", true, tc.info, limits.Defaults()); got != tc.want {
			t.Errorf("mcpStartupLine(%+v)\n got  %q\n want %q", tc.info, got, tc.want)
		}
	}
}

// TestMCPCommand_HasTheLimitFlag: --limit is a repeatable string slice.
func TestMCPCommand_HasTheLimitFlag(t *testing.T) {
	for _, f := range MCPCommand("test").Flags {
		if slices.Contains(f.Names(), "limit") {
			if _, ok := f.(*cli.StringSliceFlag); !ok {
				t.Fatalf("--limit is %T, want *cli.StringSliceFlag", f)
			}
			return
		}
	}
	t.Fatal("`pulse mcp` has no --limit flag")
}

// TestParseLimitFlags_Good: every accepted spelling lands on its field;
// unset names stay 0 (default / profile layer); a later flag wins.
func TestParseLimitFlags_Good(t *testing.T) {
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

// TestMCPStartupLine_EchoesTunedLimits: only limits off their default
// are echoed, in the --limit grammar.
func TestMCPStartupLine_EchoesTunedLimits(t *testing.T) {
	l := limits.Defaults()
	l.MaxGroups = 1_000
	l.RequestTimeout = 30 * time.Second
	got := mcpStartupLine("/d", true, mcpserve.ServeInfo{CohortScan: true}, l)
	want := "pulse mcp: serving over stdio (data dir: /d, bind-on-open: true, cohort-scan: true, limits: request_timeout=30s max_groups=1000)"
	if got != want {
		t.Errorf("startup line\n got  %q\n want %q", got, want)
	}
}

// TestMCPPulseOptions_CarriesLimits: the --limit layer reaches the
// instance — Options.Limits wins over a profile per key, and an unset
// key still resolves to the default.
func TestMCPPulseOptions_CarriesLimits(t *testing.T) {
	t.Setenv("PULSE_FEATURE_PROFILE", "")
	opts, err := mcpPulseOptions(t.TempDir(), []string{"max_groups=42", "request_timeout=5s"})
	if err != nil {
		t.Fatalf("mcpPulseOptions: %v", err)
	}
	p, err := mcpserve.NewPulse(opts, mcpserve.Options{})
	if err != nil {
		t.Fatalf("NewPulse: %v", err)
	}
	got := p.Limits()
	if got.MaxGroups != 42 || got.RequestTimeout != 5*time.Second || got.MaxMatrixDim != limits.DefaultMaxMatrixDim {
		t.Fatalf("effective limits = %+v", got)
	}
	if _, err := mcpPulseOptions("/d", []string{"nope=1"}); err == nil {
		t.Fatal("an unknown --limit name built options")
	}
}
