package cli

import (
	"slices"
	"strings"
	"testing"

	cli "github.com/urfave/cli/v3"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
)

// flagUsages returns the Usage of every flag named name anywhere under
// root (a flag mounted on several leaves is checked on each).
func flagUsages(root *cli.Command, name string) []string {
	var out []string
	var walk func(c *cli.Command)
	walk = func(c *cli.Command) {
		for _, f := range c.Flags {
			if slices.Contains(f.Names(), name) {
				if sf, ok := f.(interface{ GetUsage() string }); ok {
					out = append(out, sf.GetUsage())
				}
			}
		}
		for _, sub := range c.Commands {
			walk(sub)
		}
	}
	walk(root)
	return out
}

// TestFlagVocab_UsageNamesEveryLibraryValue: every enumerated flag's
// help line names every value its library lister returns, on every leaf
// mounting it — so a value added to the library cannot go unadvertised.
func TestFlagVocab_UsageNamesEveryLibraryValue(t *testing.T) {
	cases := []struct {
		root   *cli.Command
		flag   string
		values []string
	}{
		{ImportCommand(), "float", pio.FloatWidths()},
		{ConvertCommand(), "float", pio.FloatWidths()},
		{ImportCommand(), "spss-missing", strs(pio.SPSSMissingModes())},
		{ConvertCommand(), "spss-missing", strs(pio.SPSSMissingModes())},
		{ImportCommand(), "dst-policy", strs(pio.DSTPolicies())},
		{ConvertCommand(), "dst-policy", strs(pio.DSTPolicies())},
		{ImportCommand(), "format", strs(pio.Formats())},
		{ExportCommand(), "format", strs(pio.Formats())},
		{ConvertCommand(), "from", strs(pio.Formats())},
		{ConvertCommand(), "to", strs(pio.Formats())},
		{ExportCommand(), "labels", strs(types.AllLabelModes())},
		{APICommand(), "labels", strs(types.AllLabelModes())},
		{APICommand(), "mode", strs(types.AllLookupMultiplicities())},
		{ExplainCommand(), "root", strs(descriptor.ExplainRequestRoots())},
		{ExplainCommand(), "detail", strs(descriptor.ExplainDetails())},
		{RecommendCommand(), "level", strs(descriptor.Levels())},
		{RakeCommand(), "missing", pulse.RakeMissingPolicies()},
		{MCPCommand("test"), "return", strs(types.AllReturnPresets())},
		{MCPCommand("test"), "limit", pulse.LimitNames()},
		{WidenCommand(), "to", pulse.WidenSetFieldTargets()},
		{ErrorsCommand(), "domain", perr.AllDomains()},
		{&cli.Command{Name: "root", Flags: LogFlags()}, "log-level", logLevelNames},
		{&cli.Command{Name: "root", Flags: LogFlags()}, "log-format", logFormatNames},
	}
	for _, c := range cases {
		usages := flagUsages(c.root, c.flag)
		if len(usages) == 0 {
			t.Errorf("%s: no --%s flag", c.root.Name, c.flag)
		}
		for _, u := range usages {
			for _, v := range c.values {
				if !strings.Contains(u, v) {
					t.Errorf("%s --%s usage does not name %q: %s", c.root.Name, c.flag, v, u)
				}
			}
		}
	}
}

// TestFlagVocab_ExplainRootPairsMatchLister: the --root plumbing map has
// exactly the library's request-mode roots, and only sample lacks a
// response slot.
func TestFlagVocab_ExplainRootPairsMatchLister(t *testing.T) {
	roots := descriptor.ExplainRequestRoots()
	if len(explainRootPairs) != len(roots) {
		t.Fatalf("explainRootPairs has %d roots, lister %d", len(explainRootPairs), len(roots))
	}
	for _, r := range roots {
		pair, ok := explainRootPairs[r]
		if !ok {
			t.Errorf("root %q has no slot pair", r)
			continue
		}
		if (pair.response == nil) != (r == descriptor.ExplainRootSample) {
			t.Errorf("root %q: response slot present = %v", r, pair.response != nil)
		}
	}
}

// TestFlagVocab_LogVocabularyIsOneSet: the CLI-owned log spellings feed
// both the level map and the validator.
func TestFlagVocab_LogVocabularyIsOneSet(t *testing.T) {
	if logLevelNames[0] != logLevelOff || len(logLevels) != len(logLevelNames)-1 {
		t.Fatalf("logLevelNames %v vs logLevels %v", logLevelNames, logLevels)
	}
	for _, l := range logLevelNames {
		if _, err := newLogger(l, logFormatText, &strings.Builder{}); err != nil {
			t.Errorf("--log-level %s: %v", l, err)
		}
	}
	for _, f := range logFormatNames {
		if _, err := newLogger("info", f, &strings.Builder{}); err != nil {
			t.Errorf("--log-format %s: %v", f, err)
		}
	}
}
