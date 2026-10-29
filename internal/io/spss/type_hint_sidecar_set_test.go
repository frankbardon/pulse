package spss

import (
	"bytes"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/spsstest"
	"github.com/spf13/afero"
)

// TestTypeHint_SidecarPathKeepsSetRung: a set column WIDER than its members
// need reaches the sidecar path without any widen — a hinted `.sav` import
// writes the cohort at the hinted rung AND a fresh metadata sidecar. The
// sidecar-path export folds the derived set back into its constituents, so
// before the fix it wrote no rung hint and the re-import narrowed the set
// silently (set_u16 → set_u8). It now hints the rung on the constituent
// standing for bit 0, and the set comes back at its rung with every
// selection intact. A set already at its narrowest rung gets no hint.
func TestTypeHint_SidecarPathKeepsSetRung(t *testing.T) {
	t.Parallel()

	selections := [][]int{{0, 2}, {}, {1}}
	for _, ft := range []encoding.FieldType{
		encoding.FieldTypeSetU8, encoding.FieldTypeSetU16, encoding.FieldTypeSetU64, encoding.FieldTypeSetU256,
	} {
		t.Run(ft.String(), func(t *testing.T) {
			t.Parallel()
			fs, path := wideSetCohort(t, ft, 3, selections)
			if err := afero.WriteFile(fs, "first.sav", exportSav(t, fs, path, "first.sav", WriterOptions{}), 0o644); err != nil {
				t.Fatal(err)
			}
			importSavReport(t, fs, "first.sav", "mid.pulse")
			if got := cohortSchema(t, fs, "mid.pulse").Field("wide").Type; got != ft {
				t.Fatalf("the hinted import wrote %s, want %s", got, ft)
			}
			res, err := LoadSidecar(fs, "mid.pulse", WriterOptions{})
			if err != nil || res.Status != SidecarStatusPresent {
				t.Fatalf("LoadSidecar = %v, %v; the second export must take the sidecar path", res, err)
			}

			sav := exportSav(t, fs, "mid.pulse", "second.sav", WriterOptions{})
			hinted := bytes.Contains(sav, []byte(pulseTypeAttr+"('"+ft.String()+"'"))
			if want := ft != encoding.FieldTypeSetU8; hinted != want {
				t.Errorf("the sidecar-path .sav carries a %s hint = %v, want %v", ft, hinted, want)
			}
			if err := afero.WriteFile(fs, "second.sav", sav, 0o644); err != nil {
				t.Fatal(err)
			}
			_, warns := importSavReport(t, fs, "second.sav", "back.pulse")
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

// threeMemberSpec is a 3-dichotomy multiple-response set $opts (M0..M2) plus
// one unrelated numeric, with the given record 7/18 text.
func threeMemberSpec(attrs string) spsstest.Spec {
	num := spsstest.Format{Type: spsstest.FormatF, Width: 1}
	var (
		vars  []spsstest.Var
		names []string
	)
	for i := 0; i < 3; i++ {
		n := "M" + itoa(i)
		vars = append(vars, spsstest.Var{Name: n, Label: "Option " + itoa(i), Print: num})
		names = append(names, n)
	}
	vars = append(vars, spsstest.Var{Name: "OTHER", Label: "Other", Print: num})
	return spsstest.Spec{
		Vars: vars,
		Cases: [][]spsstest.Value{
			{spsstest.Num(1), spsstest.Num(0), spsstest.Num(1), spsstest.Num(7)},
			{spsstest.Num(0), spsstest.Num(1), spsstest.Num(0), spsstest.Num(8)},
		},
		MultipleResponseSets: []spsstest.MRSet{{
			Name: "$opts", Kind: spsstest.MRDichotomy, Label: "Options",
			CountedValue: "1", Vars: names, Subtype: spsstest.SubtypeMRSets,
		}},
		VarAttributes:     attrs,
		CharacterEncoding: "UTF-8",
	}
}

// TestTypeHint_SetRungBesideColumnHint: the bit-0 constituent of a set can
// need its OWN type hint too (a u8 dichotomy) on the sidecar path — one
// variable, two facts. Both ride one `@PulseType` attribute with two values,
// the column type then the set rung, and both come back: the member as u8,
// the set at its rung, with no warning.
func TestTypeHint_SetRungBesideColumnHint(t *testing.T) {
	t.Parallel()

	s, warns, _, fs := importSpecFS(t, threeMemberSpec("M0:@PulseType('u8'\n'set_u16'\n)"))
	if w := notRestored(warns); len(w) > 0 {
		t.Fatalf("unexpected %v", w)
	}
	if got := s.Field("M0").Type; got != encoding.FieldTypeU8 {
		t.Fatalf("M0 = %s, want u8 from the column half of the two-value hint", got)
	}
	if got := s.Field("opts").Type; got != encoding.FieldTypeSetU16 {
		t.Fatalf("opts = %s, want set_u16 from the set half of the two-value hint", got)
	}

	sav := exportSav(t, fs, "s.pulse", "again.sav", WriterOptions{})
	if want := []byte("M0:" + pulseTypeAttr + "('u8'\n'set_u16'\n)"); !bytes.Contains(sav, want) {
		t.Errorf("the sidecar-path .sav does not carry %q", want)
	}
	if err := afero.WriteFile(fs, "again.sav", sav, 0o644); err != nil {
		t.Fatal(err)
	}
	_, warns = importSavReport(t, fs, "again.sav", "back.pulse")
	if w := notRestored(warns); len(w) > 0 {
		t.Errorf("unexpected %v", w)
	}
	back := cohortSchema(t, fs, "back.pulse")
	if got := back.Field("M0").Type; got != encoding.FieldTypeU8 {
		t.Errorf("re-imported M0 = %s, want u8", got)
	}
	if got := back.Field("opts").Type; got != encoding.FieldTypeSetU16 {
		t.Errorf("re-imported opts = %s, want set_u16", got)
	}
}

// TestTypeHint_TwoValueHintMustPairColumnAndSet: a two-value `@PulseType` is
// read only as one column type plus one set rung. Two column types, two set
// rungs, or an unparseable value is unreadable: ONE
// PULSE_SPSS_TYPE_NOT_RESTORED for the variable, and the file maps exactly as
// it would with no hint at all.
func TestTypeHint_TwoValueHintMustPairColumnAndSet(t *testing.T) {
	t.Parallel()

	baseline, _, _ := importSpec(t, threeMemberSpec(""))
	for _, tc := range []struct{ name, attrs string }{
		{"two column types", "M0:@PulseType('u8'\n'u16'\n)"},
		{"two set rungs", "M0:@PulseType('set_u16'\n'set_u32'\n)"},
		{"an unparseable half", "M0:@PulseType('u8'\n'nope'\n)"},
		{"three values", "M0:@PulseType('u8'\n'set_u16'\n'u8'\n)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s, warns, _ := importSpec(t, threeMemberSpec(tc.attrs))
			w := notRestored(warns)
			if len(w) != 1 || w[0].Details["variable"] != "M0" {
				t.Fatalf("warnings = %v, want one PULSE_SPSS_TYPE_NOT_RESTORED for M0", w)
			}
			for i := range baseline.Fields {
				if s.Fields[i].Name != baseline.Fields[i].Name || s.Fields[i].Type != baseline.Fields[i].Type {
					t.Errorf("field %d = %s %s, want the hint-free %s %s", i,
						s.Fields[i].Name, s.Fields[i].Type, baseline.Fields[i].Name, baseline.Fields[i].Type)
				}
			}
		})
	}
}
