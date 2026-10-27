//go:build heavy

package pulse_test

import (
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/spf13/afero"
)

// TestParamsDeclaredMatchRead is the declared-vs-read gate behind
// strict params decode (U44 #301): the manifest's Param lists are the
// allowlist a slot's params object is refused against
// (descx.ParamsAllowlist / ParamsRefusal), so a key an operator READS
// that its list does not declare would be a false refusal of a
// working request, and a declared params key it never reads would be
// accepted and silently ignored.
//
// It runs the whole embedded example library — every root, Predict and
// execution — under a paramread.Recording (every built-in params read
// goes through internal/paramread, TestParamsReadThroughParamread) and
// holds, per built-in operator:
//
//   - every key read is a declared params key (no Slot mark);
//   - every operator declaring params keys was exercised, and read each
//     of them (a struct decode records its whole key set);
//   - every Slot-marked declaration is a json field of the slot object;
//   - no example is refused for an unknown params key.
func TestParamsDeclaredMatchRead(t *testing.T) {
	tmp := t.TempDir()
	fs := afero.NewOsFs()
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	buildExampleFixtures(t, p, fs, tmp)

	rec := paramread.Start()
	runAllExamples(t, p, tmp)
	runParamsProbes(t, p, tmp)
	read := rec.Stop()

	allow := descx.ParamsAllowlist(nil)
	var problems []string
	for k, keys := range read {
		decl, ok := allow[k]
		if !ok {
			problems = append(problems, string(k.Category)+" "+k.Op+": reads params "+strings.Join(keys, ", ")+" but is not in the allowlist")
			continue
		}
		for _, key := range keys {
			if !slices.Contains(decl.Keys, key) && !slices.Contains(decl.OwnRefusal, key) {
				problems = append(problems, string(k.Category)+" "+k.Op+": reads params."+key+", which its manifest Param list does not declare (a params key, no Slot mark)")
			}
		}
	}
	for k, decl := range allow {
		for _, s := range decl.Slot {
			st := paramsSlotTypes[k.Category]
			if st == nil || !jsonFieldOf(st, s) {
				problems = append(problems, string(k.Category)+" "+k.Op+": declares "+s+" as a Slot param, which is not a field of the slot object")
			}
		}
		if len(decl.Keys) == 0 {
			continue
		}
		got, exercised := read[k]
		if !exercised {
			problems = append(problems, string(k.Category)+" "+k.Op+": declares params "+strings.Join(decl.Keys, ", ")+" but no example run read its params (exercise it, or mark a slot field Slot)")
			continue
		}
		for _, key := range decl.Keys {
			if !slices.Contains(got, key) {
				problems = append(problems, string(k.Category)+" "+k.Op+": declares params."+key+" but the runtime never reads it (a slot field: mark it Slot; otherwise drop it)")
			}
		}
	}
	sort.Strings(problems)
	for _, pr := range problems {
		t.Error(pr)
	}
}
