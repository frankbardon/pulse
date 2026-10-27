package regmmm_test

import (
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
)

// TestMMMSkills_ServedOnStockInstance: the media-mix skills ship with
// Pulse's own pack — served on a stock instance with nothing registered
// (they moved here with the models when the contrib pack was retired).
func TestMMMSkills_ServedOnStockInstance(t *testing.T) {
	t.Parallel()

	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	for name, want := range map[string][]string{
		"op-reg-mmm-hill":  {"k:<channel>", "## Params"},
		"op-reg-mmm-bayes": {"## Params", "time_gap", "holdout_from"},
		"mmm-workflow":     {"lift test"},
	} {
		body, ok := p.Skill(name)
		if !ok {
			t.Errorf("skill %s not served", name)
			continue
		}
		for _, w := range want {
			if !strings.Contains(body, w) {
				t.Errorf("served %s lacks %q", name, w)
			}
		}
	}
}
