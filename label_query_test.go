package pulse

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

func newLabelPulse(t *testing.T) *Pulse {
	t.Helper()
	p, err := New(Options{
		FS: afero.NewMemMapFs(),
		Extensions: Extensions{
			LabelTables: map[string]LabelTable{
				"brand": {Rows: map[string]string{
					"1": "Nike",
					"2": "Adidas",
					"3": "New Balance",
					"4": "Reebok",
				}},
				"live_only": {Lookup: func(k string) (string, bool, error) { return "", false, nil }},
			},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func TestLabelTables_ListsEnumerableFlag(t *testing.T) {
	p := newLabelPulse(t)
	got := p.LabelTables()
	if len(got) != 2 {
		t.Fatalf("expected 2 tables; got %d (%+v)", len(got), got)
	}
	// Sorted by name: "brand" then "live_only".
	if got[0].Name != "brand" || got[0].RowCount != 4 || !got[0].Enumerable {
		t.Fatalf("brand entry wrong: %+v", got[0])
	}
	if got[1].Name != "live_only" || got[1].Enumerable {
		t.Fatalf("live_only should be non-enumerable: %+v", got[1])
	}
}

func TestResolveLabel_Ranking(t *testing.T) {
	p := newLabelPulse(t)
	// "ne" matches "New Balance" (prefix) and "Nike" (no), and substring
	// in none else; case-insensitive.
	got, err := p.ResolveLabel("brand", "new", 10)
	if err != nil {
		t.Fatalf("ResolveLabel: %v", err)
	}
	if len(got) != 1 || got[0].Value != "New Balance" || got[0].Key != "3" {
		t.Fatalf("expected New Balance/3; got %+v", got)
	}
}

func TestResolveLabel_ExactBeatsSubstring(t *testing.T) {
	p := newLabelPulse(t)
	got, err := p.ResolveLabel("brand", "Nike", 10)
	if err != nil {
		t.Fatalf("ResolveLabel: %v", err)
	}
	if len(got) == 0 || got[0].Value != "Nike" {
		t.Fatalf("expected exact Nike first; got %+v", got)
	}
}

func TestResolveLabel_KeyMatch(t *testing.T) {
	p := newLabelPulse(t)
	got, err := p.ResolveLabel("brand", "2", 10)
	if err != nil {
		t.Fatalf("ResolveLabel: %v", err)
	}
	if len(got) != 1 || got[0].Key != "2" || got[0].Value != "Adidas" {
		t.Fatalf("expected key 2 -> Adidas; got %+v", got)
	}
}

func TestResolveLabel_LimitAndBrowse(t *testing.T) {
	p := newLabelPulse(t)
	got, err := p.ResolveLabel("brand", "", 2)
	if err != nil {
		t.Fatalf("ResolveLabel: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 rows in browse mode with limit 2; got %d", len(got))
	}
}

func TestResolveLabel_Misspelling(t *testing.T) {
	p := newLabelPulse(t)
	cases := []struct {
		query string
		want  string
	}{
		{"Addidas", "Adidas"},           // adjacent transposition + doubled letter
		{"Nikee", "Nike"},               // trailing typo
		{"adiddas", "Adidas"},           // doubled letter
		{"new balanace", "New Balance"}, // typo with space
		{"Reebock", "Reebok"},           // inserted letter
	}
	for _, c := range cases {
		got, err := p.ResolveLabel("brand", c.query, 5)
		if err != nil {
			t.Fatalf("ResolveLabel(%q): %v", c.query, err)
		}
		if len(got) == 0 || got[0].Value != c.want {
			t.Fatalf("ResolveLabel(%q): top = %+v; want %q first", c.query, got, c.want)
		}
		if got[0].Score < labelMatchFloor {
			t.Fatalf("ResolveLabel(%q): top score %.3f below floor", c.query, got[0].Score)
		}
	}
}

func TestResolveLabel_ExactScoresOne(t *testing.T) {
	p := newLabelPulse(t)
	got, err := p.ResolveLabel("brand", "Adidas", 5)
	if err != nil {
		t.Fatalf("ResolveLabel: %v", err)
	}
	if got[0].Value != "Adidas" || got[0].Score != 1.0 {
		t.Fatalf("expected exact Adidas score 1.0; got %+v", got[0])
	}
}

func TestResolveLabel_GibberishNoFalsePositive(t *testing.T) {
	p := newLabelPulse(t)
	// A query with no real match must never surface a confident hit, and
	// when it does return weak candidates they are capped at the
	// closest-few fallback (a zero-overlap query may return nothing — the
	// agent then reports no match rather than inventing one).
	got, err := p.ResolveLabel("brand", "zzqwx", 5)
	if err != nil {
		t.Fatalf("ResolveLabel: %v", err)
	}
	for _, m := range got {
		if m.Score >= labelMatchFloor {
			t.Fatalf("gibberish produced a confident match: %+v", m)
		}
	}
	if len(got) > labelClosestFallback {
		t.Fatalf("expected at most %d fallback suggestions; got %d", labelClosestFallback, len(got))
	}
}

func TestResolveLabel_FallbackReturnsClosest(t *testing.T) {
	// A table + query engineered so the best match sits below the floor:
	// the fallback must still surface the closest few (capped) with their
	// low scores, so a caller can offer "did you mean".
	p, err := New(Options{
		FS: afero.NewMemMapFs(),
		Extensions: Extensions{
			LabelTables: map[string]LabelTable{
				"x": {Rows: map[string]string{
					"1": "alphabet",
					"2": "alphanumeric",
					"3": "zzzzzzzz",
				}},
			},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// "alp" is a prefix of two entries (confident) — instead use a weak,
	// sub-floor query that still shares some trigrams with the "alpha*"
	// entries but matches none well.
	got, err := p.ResolveLabel("x", "alqnumxyz", 5)
	if err != nil {
		t.Fatalf("ResolveLabel: %v", err)
	}
	if len(got) == 0 {
		t.Fatalf("expected closest-few fallback; got none")
	}
	if len(got) > labelClosestFallback {
		t.Fatalf("fallback exceeded cap: %d", len(got))
	}
}

func TestResolveLabel_ScoresDescending(t *testing.T) {
	p := newLabelPulse(t)
	got, err := p.ResolveLabel("brand", "n", 10)
	if err != nil {
		t.Fatalf("ResolveLabel: %v", err)
	}
	for i := 1; i < len(got); i++ {
		if got[i].Score > got[i-1].Score {
			t.Fatalf("scores not descending: %+v", got)
		}
	}
}

func TestResolveLabel_UnknownTable(t *testing.T) {
	p := newLabelPulse(t)
	_, err := p.ResolveLabel("missing", "x", 10)
	if !errors.HasCode(err, errors.PULSE_LABEL_TABLE_UNKNOWN) {
		t.Fatalf("expected PULSE_LABEL_TABLE_UNKNOWN; got %v", err)
	}
}

func TestResolveLabel_NotEnumerable(t *testing.T) {
	p := newLabelPulse(t)
	_, err := p.ResolveLabel("live_only", "x", 10)
	if !errors.HasCode(err, errors.PULSE_LABEL_TABLE_NOT_ENUMERABLE) {
		t.Fatalf("expected PULSE_LABEL_TABLE_NOT_ENUMERABLE; got %v", err)
	}
}
