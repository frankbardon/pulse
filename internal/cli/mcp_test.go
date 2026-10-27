package cli

import (
	"slices"
	"testing"
	"time"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/mcpserve"
	"github.com/frankbardon/pulse/types"
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
// scan off must read "cohort-scan: false", a loaded profile is named, the
// effective return preset is always shown, and the log level / metrics
// address appear only when set.
func TestMCPStartupLine_ReportsTheEffectiveSettings(t *testing.T) {
	std := types.ReturnPresetStandard
	cases := []struct {
		info mcpserve.ServeInfo
		obs  mcpObsSettings
		want string
	}{
		{mcpserve.ServeInfo{CohortScan: true, DefaultReturn: std}, mcpObsSettings{},
			"pulse mcp: serving over stdio (data dir: /d, bind-on-open: true, cohort-scan: true, return: standard)"},
		{mcpserve.ServeInfo{CohortScan: false, FeatureProfileLoaded: true, FeatureProfile: "self-serve", DefaultReturn: types.ReturnPresetMinimal}, mcpObsSettings{},
			"pulse mcp: serving over stdio (data dir: /d, bind-on-open: true, cohort-scan: false, return: minimal, feature-profile: self-serve)"},
		{mcpserve.ServeInfo{CohortScan: true, FeatureProfileLoaded: true}, mcpObsSettings{},
			"pulse mcp: serving over stdio (data dir: /d, bind-on-open: true, cohort-scan: true, return: (custom), feature-profile: (unnamed))"},
		{mcpserve.ServeInfo{CohortScan: true, DefaultReturn: types.ReturnPresetFull}, mcpObsSettings{LogLevel: "info", MetricsAddr: "127.0.0.1:9090"},
			"pulse mcp: serving over stdio (data dir: /d, bind-on-open: true, cohort-scan: true, return: full, log-level: info, metrics: 127.0.0.1:9090)"},
	}
	for _, tc := range cases {
		if got := mcpStartupLine("/d", true, tc.info, tc.obs); got != tc.want {
			t.Errorf("mcpStartupLine(%+v)\n got  %q\n want %q", tc.info, got, tc.want)
		}
	}
}

// TestMCPStartupLine_EchoesTunedLimits: only limits off their default
// are echoed, in the --limit grammar.
func TestMCPStartupLine_EchoesTunedLimits(t *testing.T) {
	t.Setenv("PULSE_FEATURE_PROFILE", "")
	l := limits.Defaults()
	l.MaxGroups = 1_000
	l.RequestTimeout = 30 * time.Second
	p, err := mcpserve.NewPulse(pulse.Options{DataDir: t.TempDir(), Limits: l}, mcpserve.Options{})
	if err != nil {
		t.Fatalf("NewPulse: %v", err)
	}
	info := mcpserve.Describe(p, mcpserve.Options{})
	got := mcpStartupLine("/d", true, info, mcpObsSettings{})
	want := "pulse mcp: serving over stdio (data dir: /d, bind-on-open: true, cohort-scan: true, return: standard, limits: request_timeout=30s max_groups=1000)"
	if got != want {
		t.Errorf("startup line\n got  %q\n want %q", got, want)
	}
}
