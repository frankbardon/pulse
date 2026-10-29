package spss

import (
	"bytes"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/spsstest"
	"github.com/spf13/afero"
)

// TestTypeHint_SetRungRoundTrips: a set column declared wider than its
// dictionary needs comes back from `.sav` at its declared rung — every rung,
// set_u8 through set_u256 — with every selection, the empty mask and the null
// intact. Before the hint a 3-member set_u16 re-imported as set_u8.
func TestTypeHint_SetRungRoundTrips(t *testing.T) {
	t.Parallel()

	selections := [][]int{{0, 2}, {}, {1}}
	for _, ft := range []encoding.FieldType{
		encoding.FieldTypeSetU8, encoding.FieldTypeSetU16, encoding.FieldTypeSetU32,
		encoding.FieldTypeSetU64, encoding.FieldTypeSetU128, encoding.FieldTypeSetU256,
	} {
		t.Run(ft.String(), func(t *testing.T) {
			t.Parallel()
			fs, path := wideSetCohort(t, ft, 3, selections)
			sav := exportSav(t, fs, path, "out.sav", WriterOptions{})
			hinted := bytes.Contains(sav, []byte(pulseTypeAttr+"('"+ft.String()+"'"))
			if want := ft != encoding.FieldTypeSetU8; hinted != want {
				t.Errorf("the .sav carries a %s hint = %v, want %v (only a rung wider than the members need is hinted)", ft, hinted, want)
			}
			if err := afero.WriteFile(fs, "out.sav", sav, 0o644); err != nil {
				t.Fatal(err)
			}
			_, warns := importSavReport(t, fs, "out.sav", "back.pulse")
			if w := notRestored(warns); len(w) > 0 {
				t.Errorf("unexpected %v", w)
			}
			if got := cohortSchema(t, fs, "back.pulse").Field("wide").Type; got != ft {
				t.Errorf("re-imported set type = %s, want %s", got, ft)
			}
			back, null := readCohortSetColumn(t, fs, "back.pulse", "wide")
			for row, sel := range selections {
				if null[row] || !back[row].Equal(maskOf(sel...)) {
					t.Errorf("row %d = %v (null %v), want %v", row, setBits(back[row]), null[row], sel)
				}
			}
		})
	}
}

// TestTypeHint_SetHintTooNarrowOrMisplacedWarns: a set hint is never taken on
// trust. A rung with fewer bits than the set has members, or a set hint on a
// variable that is not the first member of a derived set, keeps the plain
// mapping and raises one PULSE_SPSS_TYPE_NOT_RESTORED — and the member
// variable itself always maps exactly as without a hint.
func TestTypeHint_SetHintTooNarrowOrMisplacedWarns(t *testing.T) {
	t.Parallel()

	nine := func(attrs string) spsstest.Spec {
		num := spsstest.Format{Type: spsstest.FormatF, Width: 1}
		var vars []spsstest.Var
		var names []string
		row := []spsstest.Value{}
		for i := 0; i < 9; i++ {
			n := "M" + itoa(i)
			vars = append(vars, spsstest.Var{Name: n, Label: "Option " + itoa(i), Print: num})
			names = append(names, n)
			row = append(row, spsstest.Num(float64(i%2)))
		}
		vars = append(vars, spsstest.Var{Name: "OTHER", Label: "Other", Print: num})
		row = append(row, spsstest.Num(1))
		return spsstest.Spec{
			Vars:  vars,
			Cases: [][]spsstest.Value{row},
			MultipleResponseSets: []spsstest.MRSet{{
				Name: "$opts", Kind: spsstest.MRDichotomy, Label: "Options",
				CountedValue: "1", Vars: names, Subtype: spsstest.SubtypeMRSets,
			}},
			VarAttributes:     attrs,
			CharacterEncoding: "UTF-8",
		}
	}

	baseline, _, _ := importSpec(t, nine(""))
	for _, tc := range []struct{ name, attrs, variable string }{
		{"too narrow", "M0:@PulseType('set_u8'\n)", "M0"},
		{"not the first member", "M1:@PulseType('set_u16'\n)", "M1"},
		{"not a member", "OTHER:@PulseType('set_u16'\n)", "OTHER"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s, warns, _ := importSpec(t, nine(tc.attrs))
			w := notRestored(warns)
			if len(w) != 1 || w[0].Details[perr.DetailSPSSVariable] != tc.variable {
				t.Fatalf("warnings = %v, want one PULSE_SPSS_TYPE_NOT_RESTORED for %s", w, tc.variable)
			}
			if got, want := s.Field("opts").Type, encoding.FieldTypeSetU16; got != want {
				t.Errorf("set type = %s, want the plain mapping's %s", got, want)
			}
			for i := range baseline.Fields {
				if s.Fields[i].Name != baseline.Fields[i].Name || s.Fields[i].Type != baseline.Fields[i].Type {
					t.Errorf("field %d = %s %s, want the hint-free %s %s", i,
						s.Fields[i].Name, s.Fields[i].Type, baseline.Fields[i].Name, baseline.Fields[i].Type)
				}
			}
		})
	}

	// And the honoured case on the same file: the first member's hint widens.
	s, warns, _ := importSpec(t, nine("M0:@PulseType('set_u64'\n)"))
	if w := notRestored(warns); len(w) > 0 {
		t.Errorf("unexpected %v", w)
	}
	if got := s.Field("opts").Type; got != encoding.FieldTypeSetU64 {
		t.Errorf("set type = %s, want set_u64", got)
	}
	if got := s.Field("M0").Type; got != baseline.Field("M0").Type {
		t.Errorf("the hinted member maps %s, want the hint-free %s", got, baseline.Field("M0").Type)
	}
}

// TestTypeHint_VectorF32ElementsComeBackF32: SPSS has no vector type and the
// import never builds one, so a vector cannot round-trip; its vec_f32
// element variables do come back f32 (not f64), and vec_f64 elements carry
// no hint.
func TestTypeHint_VectorF32ElementsComeBackF32(t *testing.T) {
	t.Parallel()

	fs, cohort := vecCohort(t, vecSPSSSchema([]string{"a", "b"}), vecSPSSRows())
	sav, _, err := exportVecSav(t, fs, cohort, WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fs, "v.sav", sav, 0o644); err != nil {
		t.Fatal(err)
	}
	_, warns := importSavReport(t, fs, "v.sav", "back.pulse")
	if w := notRestored(warns); len(w) > 0 {
		t.Errorf("unexpected %v", w)
	}
	s := cohortSchema(t, fs, "back.pulse")
	for name, want := range map[string]encoding.FieldType{
		"e_1": encoding.FieldTypeF32, "e_2": encoding.FieldTypeF32,
		"r_a": encoding.FieldTypeF64, "r_b": encoding.FieldTypeF64,
	} {
		f := s.Field(name)
		if f == nil {
			t.Fatalf("no %s in %v", name, fieldNames(s))
		}
		if f.Type != want {
			t.Errorf("%s = %s, want %s", name, f.Type, want)
		}
	}
	if strings.Count(string(sav), pulseTypeAttr+"('f32'") != 2 {
		t.Errorf("want exactly the two vec_f32 elements hinted f32")
	}
}
