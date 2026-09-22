package cli

import (
	"slices"
	"testing"

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
