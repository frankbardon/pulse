package docgen

import (
	"strings"
	"testing"
)

// TestCLIReferenceRendersEveryLeafAndFlag: the rendered page carries a
// section per leaf, each of its flags (help aside) in that section, the
// global flags and the cross index, with table-breaking text escaped.
func TestCLIReferenceRendersEveryLeafAndFlag(t *testing.T) {
	t.Parallel()
	ref := CLIReference{
		Globals: []CLIFlag{{Names: []string{"log-level"}, Usage: "Level (off|debug)", Type: "string", TakesValue: true, Default: "off"}, {Names: []string{"help", "h"}, Usage: "show help"}},
		Leaves: []CLILeaf{
			{Path: "pulse a b", Usage: "Do <thing> *now*", Args: "COHORT", Flags: []CLIFlag{
				{Names: []string{"input", "i"}, Usage: "Input path", Type: "string", TakesValue: true, Required: true},
				{Names: []string{"data-dir"}, Usage: "Data dir", Type: "string", TakesValue: true, Env: []string{"PULSE_DATA_DIR"}},
				{Names: []string{"labels"}, Usage: "Bindings", Type: "string", TakesValue: true, Repeatable: true},
				{Names: []string{"help", "h"}, Usage: "show help"},
			}, Links: []CLILink{{Text: "page", Target: "page.md"}}},
			{Path: "pulse c", Usage: "Other", Flags: []CLIFlag{{Names: []string{"help", "h"}, Usage: "show help"}}},
		},
	}
	page := string(RenderCLIReference(ref))
	for _, want := range []string{
		"| `--log-level` | string | `off` | Level (off\\|debug) |",
		"### `pulse a b`\n\nDo &lt;thing&gt; \\*now\\*\n\nUsage: `pulse a b [options] COHORT`\n\nIn depth: [page](page.md)\n",
		"| `--input`, `-i` | string |  | **Required.** Input path |",
		"| `--data-dir` | string |  | Data dir Environment: `PULSE_DATA_DIR`. |",
		"| `--labels` | string, repeatable |  | Bindings |",
		"### `pulse c`\n\nOther\n\nUsage: `pulse c`\n\nNo flags of its own.\n",
		"| `--input`, `-i` | [`a b`](#pulse-a-b) |",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q:\n%s", want, page)
		}
	}
	if strings.Count(page, "`--help`, `-h`") != 1 {
		t.Errorf("--help belongs once, in the global flags; page:\n%s", page)
	}
}
