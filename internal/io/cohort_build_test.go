package io

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
)

// stagedDict assigns exactly the IDs Dictionary.AddWithLimit would, and
// refuses at the same ceiling with the same code; rollback forgets the
// provisional labels, commit makes them real in assignment order.
func TestStagedDict_MatchesDictionary(t *testing.T) {
	seed := func() *encoding.Dictionary {
		d := encoding.NewDictionary()
		_, _ = d.Add("a")
		return d
	}
	labels := []string{"b", "a", "c", "b", "d"}
	const max = 4

	direct := seed()
	staged := &stagedDict{dict: seed()}
	for _, l := range labels {
		wantID, wantErr := direct.AddWithLimit(l, max)
		gotID, gotErr := staged.AddWithLimit(l, max)
		if gotID != wantID || perr.HasCode(gotErr, perr.PULSE_IMPORT_CATEGORICAL_OVERFLOW) != perr.HasCode(wantErr, perr.PULSE_IMPORT_CATEGORICAL_OVERFLOW) {
			t.Fatalf("label %q: staged (%d, %v), direct (%d, %v)", l, gotID, gotErr, wantID, wantErr)
		}
	}
	if staged.dict.Count() != 1 {
		t.Fatalf("staging mutated the dictionary: %v", staged.dict.Values())
	}
	staged.rollback()
	if id, _ := staged.AddWithLimit("z", max); id != 1 {
		t.Fatalf("after rollback the next new label is ID %d, want 1", id)
	}
	staged.commit()
	if got := staged.dict.Values(); len(got) != 2 || got[1] != "z" {
		t.Fatalf("commit = %v, want [a z]", got)
	}
}
