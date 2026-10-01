package arrow

import (
	"bytes"
	"os"
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/frankbardon/pulse/encoding"
)

// TestTypeFromPulse_EveryLadderRungIsAStringList walks the CANONICAL
// set-width ladder rather than a list of rung constants written down
// here.
//
// A rung this switch does not name falls through to the default arm and
// maps to Float64 — a set_u256 column would export as a float, silently
// and per cell. Enumerating the rungs locally is what made that
// possible, so the assertion is driven by encoding.SetLadder(): a rung
// added there is covered the day it lands, with no edit in this
// package.
func TestTypeFromPulse_EveryLadderRungIsAStringList(t *testing.T) {
	want := arrow.ListOf(arrow.BinaryTypes.String)
	ladder := encoding.SetLadder()
	if len(ladder) == 0 {
		t.Fatal("encoding.SetLadder() is empty")
	}
	for _, ft := range ladder {
		got := TypeFromPulse(ft)
		if !arrow.TypeEqual(got, want) {
			t.Errorf("TypeFromPulse(%s) = %s, want %s — a set rung that is not "+
				"mapped here falls through to the default arm", ft, got, want)
		}
	}
}

// TestFieldFromPulse_EveryLadderRungIsAStringList is the same walk one
// level up, where a Pulse Field (not a bare type) is converted: the
// decimal arm must not swallow a set rung and the default arm must keep
// delegating to TypeFromPulse.
func TestFieldFromPulse_EveryLadderRungIsAStringList(t *testing.T) {
	want := arrow.ListOf(arrow.BinaryTypes.String)
	for _, ft := range encoding.SetLadder() {
		af := FieldFromPulse(encoding.Field{Name: "sel", Type: ft, Nullable: true})
		if !arrow.TypeEqual(af.Type, want) {
			t.Errorf("FieldFromPulse(%s).Type = %s, want %s", ft, af.Type, want)
		}
		if !af.Nullable {
			t.Errorf("FieldFromPulse(%s) dropped Nullable", ft)
		}
	}
}

// TestTypeToPulse_ListSeedsTheLaddersNarrowestRung pins the other half
// of the consolidation: the seed rung an Arrow LIST column maps to is
// READ OFF the ladder, not written down.
//
// An Arrow LIST<UTF8> says "a list of strings" and nothing about how
// many distinct elements the data holds, so only a data pass can pick a
// rung — that is io/infer.go's job, through the same
// encoding.SetTypeFor every other caller uses. This mapping is the
// narrowest rung as a starting point, and naming it locally is how a
// third width table gets born.
func TestTypeToPulse_ListSeedsTheLaddersNarrowestRung(t *testing.T) {
	ladder := encoding.SetLadder()
	if len(ladder) == 0 {
		t.Fatal("encoding.SetLadder() is empty")
	}
	want := ladder[0]

	for _, dt := range []arrow.DataType{
		arrow.ListOf(arrow.BinaryTypes.String),
		arrow.LargeListOf(arrow.BinaryTypes.String),
		arrow.FixedSizeListOf(2, arrow.BinaryTypes.String),
	} {
		if got := TypeToPulse(dt); got != want {
			t.Errorf("TypeToPulse(%s) = %s, want %s (encoding.SetLadder()[0])", dt, got, want)
		}
	}

	// The ladder's own ordering claim, asserted here because this
	// package depends on it: rung 0 really is the narrowest.
	for _, ft := range ladder[1:] {
		if ft.MaxSetEntries() <= want.MaxSetEntries() {
			t.Errorf("ladder rung %s holds %d entries, not more than rung 0 %s (%d)",
				ft, ft.MaxSetEntries(), want, want.MaxSetEntries())
		}
	}
}

// TestSetLadder_ArrowHasNoWidthTableOfItsOwn states the consolidation
// as a property: every width answer this package gives matches the
// canonical ladder's, so there is nothing left here that could fall
// behind it. encoding.SetTypeFor is the single selector; a rung that
// fits N elements must be the rung Arrow would land on after inference.
func TestSetLadder_ArrowHasNoWidthTableOfItsOwn(t *testing.T) {
	for _, ft := range encoding.SetLadder() {
		n := int(ft.MaxSetEntries())
		fitted, ok := encoding.SetTypeFor(n)
		if !ok {
			t.Fatalf("encoding.SetTypeFor(%d) declined, but %s holds that many", n, ft)
		}
		if fitted != ft {
			t.Errorf("encoding.SetTypeFor(%d) = %s, want %s", n, fitted, ft)
		}
		// And the Arrow type for that rung is width-blind: the external
		// form of a set is a token list at every width.
		if !arrow.TypeEqual(TypeFromPulse(fitted), TypeFromPulse(encoding.SetLadder()[0])) {
			t.Errorf("%s maps to a different Arrow type than the narrowest rung", fitted)
		}
	}
}

// TestTypes_NamesNoIndividualSetRung is the consolidation itself,
// asserted structurally: io/arrow/types.go must not name a single
// `FieldTypeSetU*` constant.
//
// A local list of rungs is a width table whether or not it is called
// one, and it fails the way every duplicated width table fails — not
// loudly, but by falling behind. The ladder lives in
// encoding.SetLadder() (and reaches io through pio.SetTypeFor); this
// package asks it rather than restating it, so `IsSet()` and
// `SetLadder()` are the only two spellings here.
func TestTypes_NamesNoIndividualSetRung(t *testing.T) {
	src, err := os.ReadFile("types.go")
	if err != nil {
		t.Fatalf("reading types.go: %v", err)
	}
	if idx := bytes.Index(src, []byte("FieldTypeSetU")); idx >= 0 {
		line := 1 + bytes.Count(src[:idx], []byte("\n"))
		t.Errorf("types.go:%d names a set rung constant. Use FieldType.IsSet() to map "+
			"every rung at once, and encoding.SetLadder() when a specific rung is "+
			"genuinely needed — a local rung list is a third width table.", line)
	}
}
