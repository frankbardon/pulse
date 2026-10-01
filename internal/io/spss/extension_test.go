package spss

import (
	"math"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/spsstest"
)

// TestParseExtensions_ReferenceFixture asserts every extension subtype this
// story interprets, read off the one fixture that carries all of them.
//
// The fixture's bytes were cross-checked against R's foreign::read.spss,
// whose C reader shares no code with anything here: it recovered the record
// 7/13 long names RespondentId and FullName, and flagged exactly one record
// as unrecognised — the deliberately-unknown subtype 4242 — which means it
// accepted the framing and subtype tags of every other extension record the
// generator emits.
func TestParseExtensions_ReferenceFixture(t *testing.T) {
	raw := build(t, spsstest.ExtensionReferenceSpec())
	d := mustParse(t, raw)

	t.Run("machine integer info 7/3", func(t *testing.T) {
		mi := d.machineInteger
		if !mi.present {
			t.Fatal("machineInteger.present = false, want true")
		}
		want := spsstest.DefaultMachineIntegerInfo()
		checks := []struct {
			field string
			got   int32
			want  int32
		}{
			{"versionMajor", mi.versionMajor, want.VersionMajor},
			{"versionMinor", mi.versionMinor, want.VersionMinor},
			{"versionRevision", mi.versionRevision, want.VersionRevision},
			{"machineCode", mi.machineCode, want.MachineCode},
			{"floatingPointRep", mi.floatingPointRep, want.FloatingPointRep},
			{"compressionCode", mi.compressionCode, want.CompressionCode},
			{"endianness", mi.endianness, want.Endianness},
			{"characterCode", mi.characterCode, want.CharacterCode},
		}
		for _, c := range checks {
			if c.got != c.want {
				t.Errorf("machineInteger.%s = %d, want %d", c.field, c.got, c.want)
			}
		}
	})

	t.Run("machine float info 7/4", func(t *testing.T) {
		mf := d.machineFloat
		if !mf.present {
			t.Fatal("machineFloat.present = false, want true")
		}
		want := spsstest.DefaultMachineFloatInfo()
		if mf.sysmis != want.SysMis || mf.highest != want.Highest || mf.lowest != want.Lowest {
			t.Errorf("machineFloat = {%v %v %v}, want {%v %v %v}",
				mf.sysmis, mf.highest, mf.lowest, want.SysMis, want.Highest, want.Lowest)
		}
		if d.sysmis != want.SysMis {
			t.Errorf("sysmis = %v, want the declared %v", d.sysmis, want.SysMis)
		}
	})

	t.Run("display parameters 7/11", func(t *testing.T) {
		if !d.hasDisplayParams {
			t.Fatal("hasDisplayParams = false, want true")
		}
		want := []displayParams{
			{present: true, measure: measureScale, width: 10, hasWidth: true, align: alignRight},
			{present: true, measure: measureNominal, width: 4, hasWidth: true, align: alignLeft},
			{present: true, measure: measureNominal, width: 12, hasWidth: true, align: alignLeft},
		}
		for i, w := range want {
			if got := d.vars[i].display; got != w {
				t.Errorf("vars[%d] (%s) display = %+v, want %+v", i, d.vars[i].name, got, w)
			}
		}
	})

	t.Run("long variable names 7/13 supersede the short name", func(t *testing.T) {
		want := []struct{ short, long, field string }{
			{"ID", "RespondentId", "RespondentId"},
			{"SEX", "", "SEX"},
			{"NAME", "FullName", "FullName"},
		}
		for i, w := range want {
			v := d.vars[i]
			if v.name != w.short {
				t.Errorf("vars[%d].name = %q, want the short name %q", i, v.name, w.short)
			}
			if v.longName != w.long {
				t.Errorf("vars[%d].longName = %q, want %q", i, v.longName, w.long)
			}
			if got := v.fieldName(); got != w.field {
				t.Errorf("vars[%d].fieldName() = %q, want %q — a declared long name supersedes the 8-byte short name", i, got, w.field)
			}
		}
	})

	t.Run("64-bit case count 7/16", func(t *testing.T) {
		if !d.hasCaseCount64 {
			t.Fatal("hasCaseCount64 = false, want true")
		}
		if d.caseCount64 != 2 {
			t.Errorf("caseCount64 = %d, want 2", d.caseCount64)
		}
	})

	t.Run("character encoding 7/20", func(t *testing.T) {
		if d.charsetName != "UTF-8" {
			t.Errorf("charsetName = %q, want %q", d.charsetName, "UTF-8")
		}
	})

	t.Run("documents are captured verbatim", func(t *testing.T) {
		if len(d.documents) != 2 {
			t.Fatalf("len(documents) = %d, want 2", len(d.documents))
		}
		for i, want := range []string{"Collected 2024-01-01.", "Second line."} {
			got := d.documents[i]
			if len(got) != documentLineLen {
				t.Errorf("documents[%d] is %d bytes, want the full %d-byte field kept verbatim", i, len(got), documentLineLen)
			}
			if strings.TrimRight(got, " ") != want {
				t.Errorf("documents[%d] = %q, want %q padded out", i, got, want)
			}
		}
	})

	t.Run("attributes are captured verbatim and uninterpreted", func(t *testing.T) {
		for _, tc := range []struct {
			subtype int32
			want    string
		}{
			{extFileAttributes, "$@Role('0'\n)\n"},
			{extVarAttributes, "ID:$@Role('0'\n)\n"},
		} {
			x, ok := d.rawExtension(tc.subtype)
			if !ok {
				t.Fatalf("no record 7/%d in the dictionary", tc.subtype)
			}
			if got := x.text(); got != tc.want {
				t.Errorf("7/%d payload = %q, want %q", tc.subtype, got, tc.want)
			}
		}
		// Capturing them must not also warn: a record every real SPSS file
		// carries is not "unknown", and warning on it would train callers
		// to ignore the channel.
		for _, w := range d.warnings {
			if w.Details[perr.DetailSPSSSubtype] == extFileAttributes ||
				w.Details[perr.DetailSPSSSubtype] == extVarAttributes {
				t.Errorf("a verbatim-captured attribute record warned: %v", w)
			}
		}
	})

	t.Run("every extension is retained verbatim", func(t *testing.T) {
		want := []int32{
			extMachineInteger, extMachineFloat, extVariableSets, extMRSets,
			extDisplayParams, extLongNames, extNumberOfCases,
			extFileAttributes, extVarAttributes, extMRSetsExtended,
			extCharacterEncoding, 4242,
		}
		if len(d.extensions) != len(want) {
			t.Fatalf("len(extensions) = %d, want %d", len(d.extensions), len(want))
		}
		for i, st := range want {
			x := d.extensions[i]
			if x.subtype != st {
				t.Errorf("extensions[%d].subtype = %d, want %d", i, x.subtype, st)
			}
			if int(x.size)*int(x.count) != len(x.payload) {
				t.Errorf("extensions[%d] declares %dx%d but holds %d payload byte(s)", i, x.size, x.count, len(x.payload))
			}
		}
	})
}

// TestParseExtensions_MultipleResponseSets is the acceptance test for the
// discriminant that E4-S4 and E4-S5 hang off. Getting it wrong would silently
// turn a positional multiple-category set into an additive set_* column.
func TestParseExtensions_MultipleResponseSets(t *testing.T) {
	raw := build(t, spsstest.ExtensionReferenceSpec())
	d := mustParse(t, raw)

	if len(d.mrSets) != 3 {
		t.Fatalf("len(mrSets) = %d, want 3", len(d.mrSets))
	}

	t.Run("dichotomy", func(t *testing.T) {
		set, ok := d.mrSets[0].(*mrDichotomySet)
		if !ok {
			t.Fatalf("mrSets[0] is %T, want *mrDichotomySet", d.mrSets[0])
		}
		if set.name != "$media" || set.label != "Media used" {
			t.Errorf("set = {%q %q}, want {%q %q}", set.name, set.label, "$media", "Media used")
		}
		if set.countedValue != "1" {
			t.Errorf("countedValue = %q, want %q — without it nothing says which value counts as selected", set.countedValue, "1")
		}
		if set.extended || set.labelFromVarLabel {
			t.Errorf("extended = %v, labelFromVarLabel = %v, want both false for a plain D set", set.extended, set.labelFromVarLabel)
		}
		if got := strings.Join(set.vars, ","); got != "ID,SEX" {
			t.Errorf("vars = %q, want %q", got, "ID,SEX")
		}
		if set.subtype != extMRSets {
			t.Errorf("subtype = %d, want %d", set.subtype, extMRSets)
		}
	})

	t.Run("category", func(t *testing.T) {
		set, ok := d.mrSets[1].(*mrCategorySet)
		if !ok {
			t.Fatalf("mrSets[1] is %T, want *mrCategorySet — a category set has no counted value, and the type is what enforces that", d.mrSets[1])
		}
		if set.name != "$brands" || set.label != "Brands" {
			t.Errorf("set = {%q %q}, want {%q %q}", set.name, set.label, "$brands", "Brands")
		}
		if got := strings.Join(set.vars, ","); got != "ID,SEX" {
			t.Errorf("vars = %q, want %q", got, "ID,SEX")
		}
	})

	t.Run("extended dichotomy from 7/19", func(t *testing.T) {
		set, ok := d.mrSets[2].(*mrDichotomySet)
		if !ok {
			t.Fatalf("mrSets[2] is %T, want *mrDichotomySet", d.mrSets[2])
		}
		if !set.extended {
			t.Error("extended = false, want true for an E-form definition")
		}
		if !set.labelFromVarLabel {
			t.Error("labelFromVarLabel = false, want true for label source 11")
		}
		if set.countedValue != "1" {
			t.Errorf("countedValue = %q, want %q", set.countedValue, "1")
		}
		if set.subtype != extMRSetsExtended {
			t.Errorf("subtype = %d, want %d", set.subtype, extMRSetsExtended)
		}
	})

	t.Run("a 7/5 variable set is not a response set", func(t *testing.T) {
		if len(d.variableSets) != 1 {
			t.Fatalf("len(variableSets) = %d, want 1", len(d.variableSets))
		}
		vs := d.variableSets[0]
		if vs.name != "demographics" {
			t.Errorf("variableSets[0].name = %q, want %q", vs.name, "demographics")
		}
		if got := strings.Join(vs.vars, ","); got != "ID,SEX" {
			t.Errorf("variableSets[0].vars = %q, want %q", got, "ID,SEX")
		}
		for _, set := range d.mrSets {
			if set.setName() == "demographics" {
				t.Error("a plain variable set was read as a multiple-response set")
			}
		}
	})
}

// TestParseExtensions_MRSetGrammar drives the text grammar directly, one
// definition at a time, because the generator can only emit definitions it
// considers legal and the reader has to survive the ones it does not.
func TestParseExtensions_MRSetGrammar(t *testing.T) {
	cases := []struct {
		name    string
		subtype int32
		payload string
		check   func(t *testing.T, d *dictionary)
	}{
		{
			name:    "a dichotomy with an empty label",
			subtype: extMRSets,
			payload: "$s=D1 1 0  A B\n",
			check: func(t *testing.T, d *dictionary) {
				set := wantDichotomy(t, d, 0)
				if set.label != "" {
					t.Errorf("label = %q, want empty", set.label)
				}
				if set.countedValue != "1" {
					t.Errorf("countedValue = %q, want %q", set.countedValue, "1")
				}
				if strings.Join(set.vars, ",") != "A,B" {
					t.Errorf("vars = %v, want [A B]", set.vars)
				}
			},
		},
		{
			name:    "a counted value that is a string, not a number",
			subtype: extMRSets,
			payload: "$s=D3 YES 3 Lbl A\n",
			check: func(t *testing.T, d *dictionary) {
				if got := wantDichotomy(t, d, 0).countedValue; got != "YES" {
					t.Errorf("countedValue = %q, want %q — the wire form does not say whether it is a number", got, "YES")
				}
			},
		},
		{
			name:    "a counted value containing a space",
			subtype: extMRSets,
			payload: "$s=D3 a b 3 Lbl A\n",
			check: func(t *testing.T, d *dictionary) {
				if got := wantDichotomy(t, d, 0).countedValue; got != "a b" {
					t.Errorf("countedValue = %q, want %q — the byte count, not a space, delimits a counted string", got, "a b")
				}
			},
		},
		{
			name:    "an E set with label source 1",
			subtype: extMRSetsExtended,
			payload: "$s=E 1 1 1 3 Lbl A\n",
			check: func(t *testing.T, d *dictionary) {
				set := wantDichotomy(t, d, 0)
				if !set.extended || set.labelFromVarLabel {
					t.Errorf("extended = %v, labelFromVarLabel = %v, want true/false", set.extended, set.labelFromVarLabel)
				}
			},
		},
		{
			name:    "several definitions in one record",
			subtype: extMRSets,
			payload: "$one=D1 1 3 One A\n$two=C 3 Two B\n",
			check: func(t *testing.T, d *dictionary) {
				if len(d.mrSets) != 2 {
					t.Fatalf("len(mrSets) = %d, want 2", len(d.mrSets))
				}
				if _, ok := d.mrSets[0].(*mrDichotomySet); !ok {
					t.Errorf("mrSets[0] is %T, want *mrDichotomySet", d.mrSets[0])
				}
				if _, ok := d.mrSets[1].(*mrCategorySet); !ok {
					t.Errorf("mrSets[1] is %T, want *mrCategorySet", d.mrSets[1])
				}
			},
		},
		{
			name:    "a bad definition does not take the rest of the record with it",
			subtype: extMRSets,
			payload: "$bad=Z1 1 3 Bad A\n$good=D1 1 4 Good B\n",
			check: func(t *testing.T, d *dictionary) {
				if len(d.mrSets) != 1 {
					t.Fatalf("len(mrSets) = %d, want 1 — the good definition after the bad one must still be read", len(d.mrSets))
				}
				if got := d.mrSets[0].setName(); got != "$good" {
					t.Errorf("mrSets[0].name = %q, want %q", got, "$good")
				}
				wantWarning(t, d, perr.PULSE_SPSS_EXTENSION_INVALID, "type code")
			},
		},
		{
			name:    "a trailing definition with no newline",
			subtype: extMRSets,
			payload: "$s=C 3 Lbl A B",
			check: func(t *testing.T, d *dictionary) {
				if len(d.mrSets) != 1 {
					t.Fatalf("len(mrSets) = %d, want 1", len(d.mrSets))
				}
				if got := strings.Join(d.mrSets[0].setVars(), ","); got != "A,B" {
					t.Errorf("vars = %q, want %q", got, "A,B")
				}
			},
		},
		{
			name:    "a member variable the dictionary does not have",
			subtype: extMRSets,
			payload: "$s=D1 1 3 Lbl A NOPE\n",
			check: func(t *testing.T, d *dictionary) {
				wantWarning(t, d, perr.PULSE_SPSS_EXTENSION_INVALID, `"NOPE"`)
			},
		},
		{
			name:    "a 7/7 record carrying a non-response definition",
			subtype: extMRSets,
			payload: "plain= A B\n",
			check: func(t *testing.T, d *dictionary) {
				if len(d.mrSets) != 0 || len(d.variableSets) != 0 {
					t.Errorf("mrSets = %d, variableSets = %d, want 0 and 0 — subtype 7 carries only response sets", len(d.mrSets), len(d.variableSets))
				}
				wantWarning(t, d, perr.PULSE_SPSS_EXTENSION_INVALID, "not a multiple-response set")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := parseWithExtensions(t, spsstest.RawExtension{
				Subtype: tc.subtype, Size: 1, Payload: []byte(tc.payload),
			})
			tc.check(t, d)
		})
	}
}

// TestParseExtensions_MRSetsMergeAcrossSubtypes proves the overlap rule: 7/19
// restates 7/7's sets with the extra label-source field rather than declaring
// new ones, so a name defined twice resolves to the LATER definition.
func TestParseExtensions_MRSetsMergeAcrossSubtypes(t *testing.T) {
	d := parseWithExtensions(t,
		spsstest.RawExtension{Subtype: extMRSets, Size: 1, Payload: []byte("$s=D1 1 3 Old A\n$only7=C 2 Se A\n")},
		spsstest.RawExtension{Subtype: extMRSetsExtended, Size: 1, Payload: []byte("$s=E 11 1 1 3 New A\n")},
	)

	if len(d.mrSets) != 2 {
		t.Fatalf("len(mrSets) = %d, want 2 — a name restated by 7/19 must not appear twice", len(d.mrSets))
	}
	set := wantDichotomy(t, d, 0)
	if set.subtype != extMRSetsExtended {
		t.Errorf("$s.subtype = %d, want %d — the later restatement wins", set.subtype, extMRSetsExtended)
	}
	if set.label != "New" || !set.labelFromVarLabel {
		t.Errorf("$s = {label %q, labelFromVarLabel %v}, want {\"New\", true}", set.label, set.labelFromVarLabel)
	}
	if d.mrSets[1].setName() != "$only7" {
		t.Errorf("mrSets[1].name = %q, want $only7 — a set 7/19 does not restate must survive", d.mrSets[1].setName())
	}
}

// TestParseExtensions_UnknownSubtypeWarnsOnce is the acceptance criterion for
// the project's leading risk: real SPSS versions emit subtypes no
// specification lists, and rejecting such a file rejects data that is
// otherwise perfectly readable.
func TestParseExtensions_UnknownSubtypeWarnsOnce(t *testing.T) {
	base := build(t, spsstest.ExtensionReferenceSpec())
	baseDict := mustParse(t, base)

	spec := spsstest.ExtensionReferenceSpec()
	spec.RawExtensions = append(spec.RawExtensions,
		spsstest.RawExtension{Subtype: 31337, Size: 4, Payload: make([]byte, 16)},
		spsstest.RawExtension{Subtype: 6, Size: 1, Payload: []byte("date variable info")},
	)
	d := mustParse(t, build(t, spec))

	unknown := map[int32]int{}
	for _, w := range d.warnings {
		if w.Code != perr.PULSE_SPSS_EXTENSION_UNKNOWN {
			continue
		}
		st, ok := w.Details[perr.DetailSPSSSubtype].(int32)
		if !ok {
			t.Fatalf("warning details subtype = %v (%T), want an int32", w.Details[perr.DetailSPSSSubtype], w.Details[perr.DetailSPSSSubtype])
		}
		unknown[st]++
	}
	for _, st := range []int32{4242, 31337, 6} {
		if unknown[st] != 1 {
			t.Errorf("subtype %d produced %d unknown-subtype warning(s), want exactly 1", st, unknown[st])
		}
	}
	if len(unknown) != 3 {
		t.Errorf("unknown subtypes warned about = %v, want exactly {4242, 31337, 6}", unknown)
	}

	// The rest of the file still parses, and every interpreted subtype still
	// landed. An unknown record is skipped, not contagious.
	if len(d.vars) != len(baseDict.vars) || d.charsetName != "UTF-8" || len(d.mrSets) != 3 {
		t.Errorf("after unknown subtypes: %d var(s), charset %q, %d mrSet(s); want %d, %q, 3",
			len(d.vars), d.charsetName, len(d.mrSets), len(baseDict.vars), "UTF-8")
	}
	if d.vars[0].fieldName() != "RespondentId" {
		t.Errorf("vars[0].fieldName() = %q, want RespondentId", d.vars[0].fieldName())
	}
	// The skipped bytes are still there.
	if x, ok := d.rawExtension(31337); !ok || len(x.payload) != 16 {
		t.Errorf("the unknown subtype's payload was not retained verbatim: %+v", x)
	}
}

// TestParseExtensions_MalformedPayloadsWarnAndSkip covers the other half of
// the tolerance policy: a subtype this reader DOES interpret, carrying a
// payload that does not match its declared shape. The framing was sound, so
// the walk is fine; only the interpretation is dropped.
func TestParseExtensions_MalformedPayloadsWarnAndSkip(t *testing.T) {
	cases := []struct {
		name  string
		ext   spsstest.RawExtension
		check func(t *testing.T, d *dictionary)
	}{
		{
			name: "7/3 with the wrong element size",
			ext:  spsstest.RawExtension{Subtype: extMachineInteger, Size: 8, Payload: make([]byte, 64)},
			check: func(t *testing.T, d *dictionary) {
				if d.machineInteger.present {
					t.Error("machineInteger.present = true, want false")
				}
			},
		},
		{
			name: "7/3 with the wrong element count",
			ext:  spsstest.RawExtension{Subtype: extMachineInteger, Size: 4, Payload: make([]byte, 12)},
			check: func(t *testing.T, d *dictionary) {
				if d.machineInteger.present {
					t.Error("machineInteger.present = true, want false")
				}
			},
		},
		{
			name: "7/4 declaring an incoherent sentinel triple",
			ext:  spsstest.RawExtension{Subtype: extMachineFloat, Size: 8, Payload: make([]byte, 24)},
			check: func(t *testing.T, d *dictionary) {
				if !d.machineFloat.present {
					t.Error("machineFloat.present = false; the raw triple must still be captured")
				}
				if d.sysmis != defaultSysmis {
					t.Errorf("sysmis = %v, want the spec default %v — a payload declaring 0 to be missing would turn every zero in the file into a null", d.sysmis, defaultSysmis)
				}
			},
		},
		{
			name: "7/11 with an element count matching no variable count",
			ext:  spsstest.RawExtension{Subtype: extDisplayParams, Size: 4, Payload: make([]byte, 20)},
			check: func(t *testing.T, d *dictionary) {
				if d.hasDisplayParams {
					t.Error("hasDisplayParams = true, want false")
				}
			},
		},
		{
			name: "7/11 declaring an out-of-range measure",
			ext: spsstest.RawExtension{Subtype: extDisplayParams, Size: 4, Payload: i32le(
				9, 8, 0, 1, 8, 0, 1, 8, 0)},
			check: func(t *testing.T, d *dictionary) {
				if !d.hasDisplayParams {
					t.Fatal("hasDisplayParams = false; the other variables' entries are still good")
				}
				if got := d.vars[0].display.measure; got != measureUnset {
					t.Errorf("vars[0].display.measure = %v, want unset — an out-of-range enum must not be carried into a smart-default switch", got)
				}
				if got := d.vars[1].display.measure; got != measureNominal {
					t.Errorf("vars[1].display.measure = %v, want nominal", got)
				}
			},
		},
		{
			name: "7/13 naming a short name the dictionary does not have",
			ext:  spsstest.RawExtension{Subtype: extLongNames, Size: 1, Payload: []byte("GHOST=Phantom\tID=Real")},
			check: func(t *testing.T, d *dictionary) {
				if d.vars[0].longName != "Real" {
					t.Errorf("vars[0].longName = %q, want Real — a bad pair must not drop the good ones", d.vars[0].longName)
				}
			},
		},
		{
			name: "7/13 mapping two short names onto one long name",
			ext:  spsstest.RawExtension{Subtype: extLongNames, Size: 1, Payload: []byte("ID=Dup\tSEX=Dup")},
			check: func(t *testing.T, d *dictionary) {
				if d.vars[0].longName != "Dup" {
					t.Errorf("vars[0].longName = %q, want Dup", d.vars[0].longName)
				}
				if d.vars[1].longName != "" {
					t.Errorf("vars[1].longName = %q, want empty — two fields cannot share a name", d.vars[1].longName)
				}
				if d.vars[1].fieldName() != "SEX" {
					t.Errorf("vars[1].fieldName() = %q, want the short name SEX", d.vars[1].fieldName())
				}
			},
		},
		{
			name: "7/13 with an entry carrying no '='",
			ext:  spsstest.RawExtension{Subtype: extLongNames, Size: 1, Payload: []byte("nonsense\tID=Real")},
			check: func(t *testing.T, d *dictionary) {
				if d.vars[0].longName != "Real" {
					t.Errorf("vars[0].longName = %q, want Real", d.vars[0].longName)
				}
			},
		},
		{
			name: "7/16 whose leading constant is not 1",
			ext:  spsstest.RawExtension{Subtype: extNumberOfCases, Size: 8, Payload: concat(i64le(7), i64le(99))},
			check: func(t *testing.T, d *dictionary) {
				if d.hasCaseCount64 {
					t.Errorf("hasCaseCount64 = true with count %d; the constant field is the payload's own sanity check", d.caseCount64)
				}
			},
		},
		{
			name: "7/16 declaring a negative case count",
			ext:  spsstest.RawExtension{Subtype: extNumberOfCases, Size: 8, Payload: concat(i64le(1), i64le(-5))},
			check: func(t *testing.T, d *dictionary) {
				if d.hasCaseCount64 {
					t.Error("hasCaseCount64 = true, want false for a negative count")
				}
			},
		},
		{
			name: "7/20 declaring an empty charset name",
			ext:  spsstest.RawExtension{Subtype: extCharacterEncoding, Size: 1, Payload: []byte("\x00\x00")},
			check: func(t *testing.T, d *dictionary) {
				if d.charsetName != "" {
					t.Errorf("charsetName = %q, want empty", d.charsetName)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := parseWithExtensions(t, tc.ext)
			wantWarning(t, d, perr.PULSE_SPSS_EXTENSION_INVALID, "")
			tc.check(t, d)
			// Whatever the payload said, the dictionary spine is intact and
			// the bytes are still there.
			if len(d.vars) != 3 {
				t.Errorf("len(vars) = %d, want 3 — a bad payload must not disturb the walk", len(d.vars))
			}
			if _, ok := d.rawExtension(tc.ext.Subtype); !ok {
				t.Error("the record was not retained verbatim")
			}
		})
	}
}

// TestParseExtensions_AbsentIsLegal is the E2-S2 guarantee restated now that
// the subtypes are interpreted: a dictionary carrying no record type 7 at all
// is legal and common, and every typed slot must degrade to its documented
// default rather than to a fault.
func TestParseExtensions_AbsentIsLegal(t *testing.T) {
	d := mustParse(t, build(t, spsstest.ReferenceSpec()))

	if len(d.extensions) != 0 {
		t.Fatalf("the reference fixture carries %d extension record(s); the no-7/* case is not being exercised", len(d.extensions))
	}
	if len(d.warnings) != 0 {
		t.Errorf("warnings = %v, want none", d.warnings)
	}
	if d.sysmis != defaultSysmis {
		t.Errorf("sysmis = %v, want the spec default %v — record 7/4 is an override, never a precondition", d.sysmis, defaultSysmis)
	}
	if d.machineInteger.present || d.machineFloat.present || d.hasDisplayParams || d.hasCaseCount64 {
		t.Error("a typed extension slot reports present with no extension records in the file")
	}
	if d.charsetName != "" || len(d.mrSets) != 0 || len(d.variableSets) != 0 || len(d.documents) != 0 {
		t.Error("a typed extension slot is populated with no extension records in the file")
	}
	for i, v := range d.vars {
		if v.longName != "" {
			t.Errorf("vars[%d].longName = %q, want empty", i, v.longName)
		}
		if v.fieldName() != v.name {
			t.Errorf("vars[%d].fieldName() = %q, want the short name %q", i, v.fieldName(), v.name)
		}
		if v.display.present {
			t.Errorf("vars[%d].display.present = true with no record 7/11", i)
		}
	}
}

// TestParseExtensions_TwoFieldDisplayParams covers the older record 7/11
// shape, which omits the display width. Which shape is in play is decided by
// the element count against the variable count; there is no flag.
func TestParseExtensions_TwoFieldDisplayParams(t *testing.T) {
	spec := spsstest.ExtensionReferenceSpec()
	spec.OmitDisplayWidth = true
	d := mustParse(t, build(t, spec))

	if !d.hasDisplayParams {
		t.Fatal("hasDisplayParams = false, want true")
	}
	want := []displayParams{
		{present: true, measure: measureScale, align: alignRight},
		{present: true, measure: measureNominal, align: alignLeft},
		{present: true, measure: measureNominal, align: alignLeft},
	}
	for i, w := range want {
		got := d.vars[i].display
		if got != w {
			t.Errorf("vars[%d] display = %+v, want %+v", i, got, w)
		}
		if got.hasWidth {
			t.Errorf("vars[%d].display.hasWidth = true; the two-field form declares no width and inventing one would be a guess", i)
		}
	}
}

// TestParseExtensions_SysmisOverride proves record 7/4 really is an override
// when the file declares a coherent triple — the other half of the
// "default when absent" rule.
func TestParseExtensions_SysmisOverride(t *testing.T) {
	declared := -1e300
	spec := spsstest.ReferenceSpec()
	spec.MachineFloatInfo = &spsstest.MachineFloatInfo{
		SysMis:  declared,
		Lowest:  -1e299,
		Highest: math.MaxFloat64,
	}
	d := mustParse(t, build(t, spec))

	if d.sysmis != declared {
		t.Errorf("sysmis = %v, want the declared %v", d.sysmis, declared)
	}
	if !d.machineFloat.present {
		t.Error("machineFloat.present = false, want true")
	}
	// A divergence from the conventional sentinel is unusual enough to say
	// out loud, since it changes which data reads as missing.
	wantWarning(t, d, perr.PULSE_SPSS_EXTENSION_INVALID, "system-missing sentinel")
}

// TestParseExtensions_DocumentsSurviveVerbatim checks the routed
// record-type-6 requirement on its own axis: E4-S1's sidecar needs the text,
// so it must not be trimmed, re-wrapped or dropped.
func TestParseExtensions_DocumentsSurviveVerbatim(t *testing.T) {
	spec := spsstest.ReferenceSpec()
	spec.Documents = []string{"first", "", "  leading and trailing  ", strings.Repeat("x", 80)}
	d := mustParse(t, build(t, spec))

	if len(d.documents) != 4 {
		t.Fatalf("len(documents) = %d, want 4", len(d.documents))
	}
	for i, line := range d.documents {
		if len(line) != documentLineLen {
			t.Errorf("documents[%d] is %d bytes, want %d", i, len(line), documentLineLen)
		}
	}
	if got := d.documents[2]; got[:25] != "  leading and trailing  "+" " {
		t.Errorf("documents[2] = %q; leading spaces and the space run were not preserved", got)
	}
	if d.documents[3] != strings.Repeat("x", 80) {
		t.Errorf("documents[3] = %q, want 80 x's with no padding", d.documents[3])
	}
	if d.documents[1] != strings.Repeat(" ", 80) {
		t.Errorf("documents[1] = %q, want an all-spaces line", d.documents[1])
	}
}

// TestParseExtensions_MalformedFramingIsStillFatal draws the line the
// tolerance policy stops at. A payload fault is a warning because it cannot
// desynchronise the walk; a FRAMING fault can, and silently resuming from a
// desynchronised offset would produce a plausible-looking dictionary that
// describes nothing in the file.
func TestParseExtensions_MalformedFramingIsStillFatal(t *testing.T) {
	base := build(t, spsstest.ReferenceSpec())
	baseDict := mustParse(t, base)

	cases := []struct {
		name   string
		inject []byte
		code   perr.Code
	}{
		{
			name:   "a negative element size",
			inject: i32le(recTypeExtension, 3, -4, 8),
			code:   perr.PULSE_SPSS_DICT_INVALID,
		},
		{
			name:   "a negative element count",
			inject: i32le(recTypeExtension, 3, 4, -8),
			code:   perr.PULSE_SPSS_DICT_INVALID,
		},
		{
			name:   "a payload longer than the file",
			inject: i32le(recTypeExtension, 3, 4, 1<<20),
			code:   perr.PULSE_SPSS_DICT_TRUNCATED,
		},
		{
			name:   "a document record longer than the file",
			inject: i32le(recTypeDocument, 1<<20),
			code:   perr.PULSE_SPSS_DICT_TRUNCATED,
		},
		{
			name:   "a negative document line count",
			inject: i32le(recTypeDocument, -1),
			code:   perr.PULSE_SPSS_DICT_INVALID,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := splice(base, baseDict.dataOffset-8, tc.inject)
			_, err := parseDictionary(raw)
			if err == nil {
				t.Fatal("parseDictionary succeeded; a framing fault desynchronises every following record")
			}
			ce := codedError(t, err)
			if ce.Code != tc.code {
				t.Errorf("code = %s, want %s: %v", ce.Code, tc.code, ce.Message)
			}
			assertDetails(t, ce, len(raw))
		})
	}
}

// --- helpers ---------------------------------------------------------------

// parseWithExtensions parses the reference fixture with the given raw
// extension records appended, so a test can supply a payload the generator's
// own validation would refuse to build.
func parseWithExtensions(t *testing.T, exts ...spsstest.RawExtension) *dictionary {
	t.Helper()
	spec := spsstest.ReferenceSpec()
	spec.RawExtensions = exts
	return mustParse(t, build(t, spec))
}

// wantDichotomy asserts that mrSets[i] is a dichotomy and returns it. The
// assertion is the point: a category set reaching a dichotomy consumer is the
// silent corruption this representation exists to prevent.
func wantDichotomy(t *testing.T, d *dictionary, i int) *mrDichotomySet {
	t.Helper()
	if len(d.mrSets) <= i {
		t.Fatalf("len(mrSets) = %d, want more than %d", len(d.mrSets), i)
	}
	set, ok := d.mrSets[i].(*mrDichotomySet)
	if !ok {
		t.Fatalf("mrSets[%d] is %T, want *mrDichotomySet", i, d.mrSets[i])
	}
	return set
}

// wantWarning asserts that at least one warning carries the given code, and
// the given substring when one is supplied. It also checks the details every
// extension diagnostic must carry.
func wantWarning(t *testing.T, d *dictionary, code perr.Code, substr string) {
	t.Helper()
	for _, w := range d.warnings {
		if w.Code != code || (substr != "" && !strings.Contains(w.Message, substr)) {
			continue
		}
		if _, ok := w.Details[perr.DetailSPSSSubtype].(int32); !ok {
			t.Errorf("warning details lack an int32 %q: %v", perr.DetailSPSSSubtype, w.Details)
		}
		if _, ok := w.Details[perr.DetailSPSSOffset].(int); !ok {
			t.Errorf("warning details lack an int %q: %v", perr.DetailSPSSOffset, w.Details)
		}
		if rec, _ := w.Details[perr.DetailSPSSRecord].(string); rec != "7" {
			t.Errorf("warning details record_type = %q, want \"7\"", rec)
		}
		return
	}
	t.Errorf("no %s warning containing %q; got %v", code, substr, d.warnings)
}
