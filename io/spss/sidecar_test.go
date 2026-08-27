package spss

import (
	"context"
	stdjson "encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/spsstest"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// importFixture runs a real import of the spec through the shared
// ImportJob path and returns the filesystem, the cohort path and the
// decoded sidecar document.
//
// It goes through pio.ImportJob rather than calling WriteSidecar
// directly on purpose: the story is "write-on-import", so the thing
// under test is what an import actually leaves on disk.
func importFixture(t *testing.T, spec spsstest.Spec) (afero.Fs, string, *Document) {
	t.Helper()
	fs, cohort := importFixtureNoSidecar(t, spec)
	return fs, cohort, readSidecar(t, fs, cohort)
}

func importFixtureNoSidecar(t *testing.T, spec spsstest.Spec) (afero.Fs, string) {
	t.Helper()
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "in.sav", build(t, spec), 0644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	job := pio.NewImportJob(NewReader(fs, "in.sav"), "out.pulse")
	job.FS = fs
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("ImportJob.Run: %v", err)
	}
	return fs, "out.pulse"
}

func readSidecar(t *testing.T, fs afero.Fs, cohort string) *Document {
	t.Helper()
	raw, err := afero.ReadFile(fs, SidecarPath(cohort))
	if err != nil {
		t.Fatalf("reading sidecar %s: %v", SidecarPath(cohort), err)
	}
	var doc Document
	if err := stdjson.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decoding sidecar: %v", err)
	}
	return &doc
}

// varOf returns the sidecar entry for the named Pulse field.
func varOf(t *testing.T, doc *Document, name string) Variable {
	t.Helper()
	for _, v := range doc.Payload.Variables {
		if v.Name == name {
			return v
		}
	}
	t.Fatalf("sidecar has no variable %q", name)
	return Variable{}
}

// richSpec is a fixture exercising most of what the sidecar has to
// carry: a labelled numeric, an unlabelled numeric, a string, a
// weight, documents, both attribute records, both multiple-response
// set flavours, a variable set, display parameters and a declared
// charset.
func richSpec() spsstest.Spec {
	return spsstest.Spec{
		Vars: []spsstest.Var{
			{
				Name: "Q1", LongName: "Satisfaction", Label: "Overall satisfaction",
				Measure: spsstest.MeasureOrdinal, DisplayWidth: 6, Align: spsstest.AlignRight,
				Print: spsstest.Format{Type: spsstest.FormatF, Width: 8, Decimals: 0},
			},
			{
				Name: "WT", Label: "Design weight", Measure: spsstest.MeasureScale,
				Print: spsstest.Format{Type: spsstest.FormatF, Width: 8, Decimals: 3},
			},
			{
				Name: "REGION", Width: 6, Label: "Region", Measure: spsstest.MeasureNominal,
			},
			{Name: "MD1", Measure: spsstest.MeasureNominal},
			{Name: "MD2", Measure: spsstest.MeasureNominal},
		},
		Cases: [][]spsstest.Value{
			{spsstest.Num(1), spsstest.Num(1.5), spsstest.Text("North"), spsstest.Num(1), spsstest.Num(0)},
			{spsstest.Num(5), spsstest.Num(0.5), spsstest.Text("South"), spsstest.Num(0), spsstest.Num(1)},
			{spsstest.Num(9), spsstest.Num(2.5), spsstest.Text("North"), spsstest.Num(1), spsstest.Num(1)},
		},
		ValueLabels: []spsstest.ValueLabelSet{{
			Vars: []string{"Q1"},
			Labels: []spsstest.ValueLabel{
				{Value: spsstest.Num(1), Label: "Very dissatisfied"},
				{Value: spsstest.Num(5), Label: "Neutral"},
				{Value: spsstest.Num(7), Label: "Never observed"},
			},
		}},
		WeightVar:         "WT",
		FileLabel:         "Wave 3",
		Documents:         []string{"Fielded 2024-01", "Weighted to census"},
		DisplayParams:     true,
		CharacterEncoding: "cp1252",
		FileAttributes:    "$@Survey('Wave 3')\n",
		VarAttributes:     "Q1:$@Origin('core')\n",
		MultipleResponseSets: []spsstest.MRSet{
			{
				Name: "$brands", Kind: spsstest.MRDichotomy, Label: "Brands used",
				CountedValue: "1", Vars: []string{"MD1", "MD2"}, Subtype: 7,
			},
			{
				Name: "$ranks", Kind: spsstest.MRCategory, Label: "Ranked",
				Vars: []string{"MD1", "MD2"}, Subtype: 7,
			},
		},
		VariableSets: []spsstest.VariableSet{
			{Name: "Demographics", Vars: []string{"REGION"}},
		},
	}
}

// ---------------------------------------------------------------------------
// Envelope, path and the flat / liftable constraint
// ---------------------------------------------------------------------------

func TestSidecarPath_FollowsImportsConvention(t *testing.T) {
	// The imports.Sidecar convention is "suffix appended to the cohort
	// filename", not "replace the extension" — a cohort and its sidecar
	// stay adjacent and sort together.
	for _, tc := range []struct{ cohort, want string }{
		{"data.pulse", "data.pulse.spss.json"},
		{"a/b/data.pulse", "a/b/data.pulse.spss.json"},
		{"no-extension", "no-extension.spss.json"},
	} {
		if got := SidecarPath(tc.cohort); got != tc.want {
			t.Errorf("SidecarPath(%q) = %q, want %q", tc.cohort, got, tc.want)
		}
	}
}

func TestSidecarSuffix_DoesNotCollideWithManagedImportSidecar(t *testing.T) {
	// imports.SidecarSuffix is ".meta.json" and a managed import writes
	// it for the same cohort. Sharing the suffix would have one artefact
	// silently overwrite the other.
	if SidecarSuffix == ".meta.json" {
		t.Fatal("SidecarSuffix collides with imports.SidecarSuffix; both are written for the same cohort")
	}
	if !strings.HasSuffix(SidecarSuffix, ".json") {
		t.Errorf("SidecarSuffix = %q, want a .json document", SidecarSuffix)
	}
}

func TestSidecar_EnvelopeIsVersionedAndKinded(t *testing.T) {
	_, _, doc := importFixture(t, richSpec())
	if doc.FormatVersion != SidecarFormatVersion {
		t.Errorf("format_version = %d, want %d", doc.FormatVersion, SidecarFormatVersion)
	}
	if doc.Kind != SidecarKind {
		t.Errorf("kind = %q, want %q", doc.Kind, SidecarKind)
	}
}

// TestSidecar_PayloadIsSelfContained is the flat / liftable constraint
// as an executable check rather than a comment.
//
// A `.pulse` schema metadata block was deferred, not rejected; keeping
// the door open requires that Payload can be serialised into one
// VERBATIM. So Payload must carry no filesystem path and no reference
// to anything outside itself — everything file-bound belongs in the
// sibling Fingerprint block, which a lift would simply drop.
func TestSidecar_PayloadIsSelfContained(t *testing.T) {
	fs, cohort, doc := importFixture(t, richSpec())

	payload, err := stdjson.Marshal(doc.Payload)
	if err != nil {
		t.Fatalf("marshalling payload: %v", err)
	}
	body := string(payload)
	for _, forbidden := range []string{cohort, SidecarPath(cohort), "in.sav", ".pulse", ".sav", ".json"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("payload references %q; it must be liftable into a .pulse metadata block with no dependence on being a separate file\n%s",
				forbidden, body)
		}
	}

	// And the fingerprint really does live outside the payload.
	var generic map[string]stdjson.RawMessage
	raw, err := afero.ReadFile(fs, SidecarPath(cohort))
	if err != nil {
		t.Fatalf("reading sidecar: %v", err)
	}
	if err := stdjson.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("decoding sidecar: %v", err)
	}
	for _, key := range []string{"format_version", "kind", "fingerprint", "payload"} {
		if _, ok := generic[key]; !ok {
			t.Errorf("document is missing top-level key %q", key)
		}
	}
	if strings.Contains(string(generic["payload"]), "fingerprint") {
		t.Error("payload carries the fingerprint; a lifted block cannot hash the file containing it")
	}
}

// ---------------------------------------------------------------------------
// Fingerprint
// ---------------------------------------------------------------------------

// TestSidecar_FingerprintsTheCohortNotTheSource is the criterion that
// is easiest to get subtly wrong: the fingerprint must describe the
// `.pulse` cohort the sidecar sits beside, not the `.sav` it came from.
// The source may be long gone; the cohort is what a consumer holds.
func TestSidecar_FingerprintsTheCohortNotTheSource(t *testing.T) {
	fs, cohort, doc := importFixture(t, richSpec())

	cohortBytes, err := afero.ReadFile(fs, cohort)
	if err != nil {
		t.Fatalf("reading cohort: %v", err)
	}
	want, ferr := encoding.ComputeFingerprint(strings.NewReader(string(cohortBytes)))
	if ferr != nil {
		t.Fatalf("ComputeFingerprint: %v", ferr)
	}
	got, ok := doc.Fingerprint.Digest()
	if !ok {
		t.Fatalf("fingerprint sha256 %q is not a %d-byte digest", doc.Fingerprint.SHA256, encoding.FingerprintSize)
	}
	if got != want {
		t.Error("fingerprint does not match the cohort's bytes")
	}

	srcBytes, err := afero.ReadFile(fs, "in.sav")
	if err != nil {
		t.Fatalf("reading source: %v", err)
	}
	srcFP, ferr := encoding.ComputeFingerprint(strings.NewReader(string(srcBytes)))
	if ferr != nil {
		t.Fatalf("ComputeFingerprint: %v", ferr)
	}
	if got == srcFP {
		t.Error("fingerprint matches the .sav source; it must fingerprint the .pulse cohort")
	}

	// The O(1) staleness pair, mirroring encoding.Index's build-time
	// snapshot — this is what E5-S1's read path compares.
	info, err := fs.Stat(cohort)
	if err != nil {
		t.Fatalf("stat cohort: %v", err)
	}
	if doc.Fingerprint.SourceSize != uint64(info.Size()) {
		t.Errorf("source_size = %d, want %d", doc.Fingerprint.SourceSize, info.Size())
	}
	if doc.Fingerprint.SourceModTime != info.ModTime().UnixNano() {
		t.Errorf("source_mod_time = %d, want %d", doc.Fingerprint.SourceModTime, info.ModTime().UnixNano())
	}
	if len(doc.Fingerprint.SHA256) != 2*encoding.FingerprintSize {
		t.Errorf("sha256 is %d hex chars, want %d for a 32-byte digest",
			len(doc.Fingerprint.SHA256), 2*encoding.FingerprintSize)
	}
}

func TestFingerprintDigest_RejectsMalformed(t *testing.T) {
	for _, bad := range []string{"", "zz", strings.Repeat("a", 63), strings.Repeat("a", 66)} {
		if _, ok := (Fingerprint{SHA256: bad}).Digest(); ok {
			t.Errorf("Digest() accepted malformed sha256 %q", bad)
		}
	}
}

// ---------------------------------------------------------------------------
// The code <-> label <-> Pulse dictionary ID triple
// ---------------------------------------------------------------------------

// TestSidecar_CategoryTriple is the single most important payload.
// Pulse IDs are positional, SPSS codes are arbitrary; without the
// pairing an export invents codes and downstream `IF q1 EQ 5` silently
// addresses a different category.
func TestSidecar_CategoryTriple(t *testing.T) {
	_, _, doc := importFixture(t, richSpec())
	v := varOf(t, doc, "Satisfaction")

	if v.PulseType == "" || !strings.HasPrefix(v.PulseType, "categorical") {
		t.Fatalf("Satisfaction pulse_type = %q, want a categorical", v.PulseType)
	}
	byCode := map[float64]Category{}
	for _, c := range v.Categories {
		if !c.Numeric {
			t.Errorf("category %q on a numeric variable is not marked numeric", c.Value)
			continue
		}
		if c.Code == nil {
			t.Fatalf("numeric category %q carries no code", c.Value)
		}
		if c.Text != nil {
			t.Errorf("numeric category %q carries a text value; only one half is meaningful", c.Value)
		}
		byCode[float64(*c.Code)] = c
	}

	for _, tc := range []struct {
		code     float64
		label    string
		labelled bool
		observed bool
	}{
		// Declared and used.
		{1, "Very dissatisfied", true, true},
		{5, "Neutral", true, true},
		// Declared, never used: still occupies its ID so the file's own
		// code ordering survives an import that never saw the code.
		{7, "Never observed", true, false},
		// Used but never declared: an unlabelled code is legal SPSS and
		// is appended in first-seen order.
		{9, "", false, true},
	} {
		c, ok := byCode[tc.code]
		if !ok {
			t.Errorf("no category for SPSS code %g", tc.code)
			continue
		}
		if c.Label != tc.label {
			t.Errorf("code %g label = %q, want %q", tc.code, c.Label, tc.label)
		}
		if c.Labelled != tc.labelled {
			t.Errorf("code %g labelled = %v, want %v", tc.code, c.Labelled, tc.labelled)
		}
		if c.Observed != tc.observed {
			t.Errorf("code %g observed = %v, want %v", tc.code, c.Observed, tc.observed)
		}
	}

	// The cohort dictionary holds CODES, not labels (E2-S6), so the
	// sidecar is the only place the labels live. Assert the ID really
	// does address the cohort's dictionary entry.
	r := NewReaderFromBytes(build(t, richSpec()))
	schema, err := r.PulseSchema()
	if err != nil {
		t.Fatalf("PulseSchema: %v", err)
	}
	f := schema.Field("Satisfaction")
	if f == nil || f.Dictionary == nil {
		t.Fatal("Satisfaction has no cohort dictionary")
	}
	for _, c := range v.Categories {
		if int(c.ID) >= f.Dictionary.Count() {
			t.Errorf("cohort dictionary has no entry at id %d", c.ID)
			continue
		}
		val := f.Dictionary.Resolve(c.ID)
		if val != c.Value {
			t.Errorf("id %d: cohort dictionary holds %q, sidecar says %q", c.ID, val, c.Value)
		}
		if val == c.Label && c.Label != "" {
			t.Errorf("id %d: cohort dictionary holds the LABEL %q; it must hold the code", c.ID, val)
		}
	}
}

func TestSidecar_StringCategoryCarriesTextNotCode(t *testing.T) {
	_, _, doc := importFixture(t, richSpec())
	v := varOf(t, doc, "REGION")
	if len(v.Categories) == 0 {
		t.Fatal("REGION carries no categories")
	}
	for _, c := range v.Categories {
		if c.Numeric {
			t.Errorf("string category %q marked numeric", c.Value)
		}
		if c.Text == nil {
			t.Errorf("string category %q carries no text value", c.Value)
		}
		if c.Code != nil {
			t.Errorf("string category %q carries a numeric code; only one half is meaningful", c.Value)
		}
	}
}

// ---------------------------------------------------------------------------
// Missing-value specs — all three shapes
// ---------------------------------------------------------------------------

// TestBuildMissing_AllThreeShapes drives the conversion directly.
//
// The fixture generator has no record-type-2 missing-value slot (only
// the record 7/22 long-string form), so the three numeric shapes are
// exercised against the parsed struct they land in. That is the same
// value the dictionary walk produces; nothing is stubbed but the walk.
func TestBuildMissing_AllThreeShapes(t *testing.T) {
	slot := func(vals ...float64) [][elementSize]byte {
		out := make([][elementSize]byte, 0, len(vals))
		for _, v := range vals {
			var b [elementSize]byte
			bits := math.Float64bits(v)
			for i := 0; i < elementSize; i++ {
				b[i] = byte(bits >> (8 * i))
			}
			out = append(out, b)
		}
		return out
	}

	tests := []struct {
		name         string
		spec         missingSpec
		wantKind     string
		wantRange    *MissingRange
		wantDiscrete []float64
		wantText     []string
	}{
		{
			name:     "none",
			spec:     missingSpec{code: 0},
			wantKind: "",
		},
		{
			name: "one discrete",
			spec: missingSpec{
				code: 1, raw: slot(9), numeric: []float64{9},
			},
			wantKind:     "discrete",
			wantDiscrete: []float64{9},
		},
		{
			name: "three discrete — the maximum the format allows",
			spec: missingSpec{
				code: 3, raw: slot(7, 8, 9), numeric: []float64{7, 8, 9},
			},
			wantKind:     "discrete",
			wantDiscrete: []float64{7, 8, 9},
		},
		{
			name: "range",
			spec: missingSpec{
				code: -2, raw: slot(90, 99), numeric: []float64{90, 99},
			},
			wantKind:  "range",
			wantRange: &MissingRange{Low: 90, High: 99},
		},
		{
			name: "range plus one discrete",
			spec: missingSpec{
				code: -3, raw: slot(90, 99, -1), numeric: []float64{90, 99, -1},
			},
			wantKind:     "range_plus_discrete",
			wantRange:    &MissingRange{Low: 90, High: 99},
			wantDiscrete: []float64{-1},
		},
		{
			name: "discrete strings — the record 7/22 shape",
			spec: missingSpec{
				code: 2, raw: slot(0, 0), text: []string{"REF", "DK"},
			},
			wantKind: "discrete",
			wantText: []string{"REF", "DK"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := buildMissing(variable{missing: tc.spec})
			if tc.wantKind == "" {
				if got != nil {
					t.Fatalf("want nil for a variable declaring no missing values, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("want a missing spec, got nil")
			}
			if got.Kind != tc.wantKind {
				t.Errorf("kind = %q, want %q", got.Kind, tc.wantKind)
			}
			if got.Code != tc.spec.code {
				t.Errorf("code = %d, want %d", got.Code, tc.spec.code)
			}
			if (got.Range == nil) != (tc.wantRange == nil) {
				t.Fatalf("range = %+v, want %+v", got.Range, tc.wantRange)
			}
			if tc.wantRange != nil && *got.Range != *tc.wantRange {
				t.Errorf("range = %+v, want %+v", *got.Range, *tc.wantRange)
			}
			if len(got.Discrete) != len(tc.wantDiscrete) {
				t.Fatalf("discrete = %v, want %v", got.Discrete, tc.wantDiscrete)
			}
			for i, want := range tc.wantDiscrete {
				if float64(got.Discrete[i]) != want {
					t.Errorf("discrete[%d] = %v, want %v", i, got.Discrete[i], want)
				}
			}
			if len(got.DiscreteText) != len(tc.wantText) {
				t.Fatalf("discrete_text = %v, want %v", got.DiscreteText, tc.wantText)
			}
			for i, want := range tc.wantText {
				if got.DiscreteText[i] != want {
					t.Errorf("discrete_text[%d] = %q, want %q", i, got.DiscreteText[i], want)
				}
			}
			// The raw slots are the authoritative record: the decoded
			// fields above are a projection, never a replacement.
			if len(got.Raw) != len(tc.spec.raw) {
				t.Errorf("raw slots = %d, want %d", len(got.Raw), len(tc.spec.raw))
			}
			for i := range got.Raw {
				if len(got.Raw[i]) != elementSize {
					t.Errorf("raw[%d] is %d bytes, want %d", i, len(got.Raw[i]), elementSize)
				}
			}
		})
	}
}

// TestSidecar_LongStringMissingRoundTrips proves the record 7/22 shape
// reaches the JSON, since that is the one the fixture generator can
// actually emit.
func TestSidecar_LongStringMissingRoundTrips(t *testing.T) {
	spec := spsstest.Spec{
		Vars: []spsstest.Var{{Name: "COMMENT", Width: 40, Label: "Free text"}},
		Cases: [][]spsstest.Value{
			{spsstest.Text("all good")},
			{spsstest.Text("REFUSED")},
		},
		LongStringMissingValues: []spsstest.LongStringMissingValues{
			{Var: "COMMENT", Values: []string{"REFUSED", "DK"}},
		},
	}
	_, _, doc := importFixture(t, spec)
	v := varOf(t, doc, "COMMENT")
	if v.Missing == nil {
		t.Fatal("COMMENT carries no missing spec")
	}
	if v.Missing.Kind != "discrete" {
		t.Errorf("kind = %q, want discrete", v.Missing.Kind)
	}
	want := []string{"REFUSED", "DK"}
	if len(v.Missing.DiscreteText) != len(want) {
		t.Fatalf("discrete_text = %v, want %v", v.Missing.DiscreteText, want)
	}
	for i := range want {
		if v.Missing.DiscreteText[i] != want[i] {
			t.Errorf("discrete_text[%d] = %q, want %q", i, v.Missing.DiscreteText[i], want[i])
		}
	}
	if len(v.Missing.Raw) != len(want) {
		t.Errorf("raw slots = %d, want %d", len(v.Missing.Raw), len(want))
	}
}

// ---------------------------------------------------------------------------
// Everything the criterion list names
// ---------------------------------------------------------------------------

func TestSidecar_CoversTheDictionaryElementsPulseCannotHold(t *testing.T) {
	_, _, doc := importFixture(t, richSpec())
	p := doc.Payload

	t.Run("product name and compression bias and nominal case size", func(t *testing.T) {
		if p.Source.ProductName == "" {
			t.Error("product_name is empty")
		}
		if float64(p.Source.CompressionBias) != spsstest.CompressionBias {
			t.Errorf("compression_bias = %v, want %v", p.Source.CompressionBias, spsstest.CompressionBias)
		}
		if p.Source.NominalCaseSize == 0 {
			t.Error("nominal_case_size is zero")
		}
		// The header's claim and the authoritative count are both kept
		// so a disagreement stays visible rather than being resolved.
		if p.Source.ElementCount == 0 {
			t.Error("element_count is zero")
		}
		if p.Source.FileLabel != "Wave 3" {
			t.Errorf("file_label = %q, want %q", p.Source.FileLabel, "Wave 3")
		}
		if p.Source.CaseCount != 3 {
			t.Errorf("case_count = %d, want 3", p.Source.CaseCount)
		}
	})

	t.Run("weight variable", func(t *testing.T) {
		if p.Weight == nil {
			t.Fatal("weight is nil")
		}
		if p.Weight.Variable != "WT" {
			t.Errorf("weight variable = %q, want WT", p.Weight.Variable)
		}
		if p.Weight.Index == 0 {
			t.Error("weight index is zero")
		}
	})

	t.Run("document records", func(t *testing.T) {
		if len(p.Documents) != 2 {
			t.Fatalf("documents = %v, want 2 lines", p.Documents)
		}
		// Captured as the full fixed-width 80-byte field, untrimmed:
		// which trailing spaces are padding is not knowable.
		for i, line := range p.Documents {
			if len(line) != documentLineLen {
				t.Errorf("documents[%d] is %d bytes, want the untrimmed %d-byte field",
					i, len(line), documentLineLen)
			}
			if !strings.HasPrefix(line, []string{"Fielded 2024-01", "Weighted to census"}[i]) {
				t.Errorf("documents[%d] = %q", i, line)
			}
		}
	})

	t.Run("file and variable attributes stay distinct", func(t *testing.T) {
		if p.FileAttributes == nil {
			t.Fatal("file_attributes is nil")
		}
		if p.VariableAttributes == nil {
			t.Fatal("variable_attributes is nil")
		}
		if p.FileAttributes.Subtype != extFileAttributes {
			t.Errorf("file_attributes subtype = %d, want %d", p.FileAttributes.Subtype, extFileAttributes)
		}
		if p.VariableAttributes.Subtype != extVarAttributes {
			t.Errorf("variable_attributes subtype = %d, want %d", p.VariableAttributes.Subtype, extVarAttributes)
		}
		if string(p.FileAttributes.Raw) != "$@Survey('Wave 3')\n" {
			t.Errorf("file_attributes raw = %q", p.FileAttributes.Raw)
		}
		if string(p.VariableAttributes.Raw) != "Q1:$@Origin('core')\n" {
			t.Errorf("variable_attributes raw = %q", p.VariableAttributes.Raw)
		}
		// The two records say different things; merging them would lose
		// which was which.
		if string(p.FileAttributes.Raw) == string(p.VariableAttributes.Raw) {
			t.Error("7/17 and 7/18 payloads are indistinguishable")
		}
	})

	t.Run("source charset keeps the file's own spelling", func(t *testing.T) {
		// E5-S4 re-encodes against the DECLARED form. A file that said
		// cp1252 must still say cp1252 here, not windows-1252.
		if p.Charset.DeclaredName != "cp1252" {
			t.Errorf("declared_name = %q, want cp1252 (the file's own spelling)", p.Charset.DeclaredName)
		}
		if !p.Charset.Declared {
			t.Error("declared = false for a file carrying record 7/20")
		}
		if p.Charset.Overridden {
			t.Error("overridden = true without a WithCharset override")
		}
		if p.Charset.ResolvedName == "" {
			t.Error("resolved_name is empty")
		}
	})

	t.Run("MC set definitions keep their two flavours apart", func(t *testing.T) {
		if len(p.MultipleResponseSets) != 2 {
			t.Fatalf("multiple_response_sets = %d, want 2", len(p.MultipleResponseSets))
		}
		byName := map[string]MRSet{}
		for _, s := range p.MultipleResponseSets {
			byName[s.Name] = s
		}
		d, ok := byName["$brands"]
		if !ok {
			t.Fatal("no $brands set")
		}
		if d.Kind != "dichotomy" {
			t.Errorf("$brands kind = %q, want dichotomy", d.Kind)
		}
		if d.CountedValue == nil || *d.CountedValue != "1" {
			t.Errorf("$brands counted_value = %v, want \"1\"", d.CountedValue)
		}
		c, ok := byName["$ranks"]
		if !ok {
			t.Fatal("no $ranks set")
		}
		if c.Kind != "category" {
			t.Errorf("$ranks kind = %q, want category", c.Kind)
		}
		// A multiple-category set has no counted value at all. The
		// pointer is what stops a consumer that forgets to check Kind
		// from reading a meaningless empty string.
		if c.CountedValue != nil {
			t.Errorf("$ranks carries a counted_value %q; a category set has none", *c.CountedValue)
		}
		if len(c.Variables) != 2 {
			t.Errorf("$ranks variables = %v, want 2 members", c.Variables)
		}
	})

	t.Run("variable sets are sidecar-only and not response sets", func(t *testing.T) {
		if len(p.VariableSets) != 1 {
			t.Fatalf("variable_sets = %v, want 1", p.VariableSets)
		}
		if p.VariableSets[0].Name != "Demographics" {
			t.Errorf("variable set name = %q", p.VariableSets[0].Name)
		}
		for _, s := range p.MultipleResponseSets {
			if s.Name == "Demographics" {
				t.Error("a variable set was recorded as a multiple-response set")
			}
		}
	})

	t.Run("per-variable metadata", func(t *testing.T) {
		q1 := varOf(t, doc, "Satisfaction")

		// Original short name — record 7/13 is a short -> long mapping
		// and an export has to write the short name back.
		if q1.ShortName != "Q1" {
			t.Errorf("short_name = %q, want Q1", q1.ShortName)
		}
		if q1.LongName != "Satisfaction" {
			t.Errorf("long_name = %q, want Satisfaction", q1.LongName)
		}
		if q1.Measure != "ordinal" {
			t.Errorf("measure = %q, want ordinal", q1.Measure)
		}
		if q1.Alignment != "right" {
			t.Errorf("alignment = %q, want right", q1.Alignment)
		}
		if !q1.HasDisplayParams {
			t.Error("has_display_params = false for a file carrying record 7/11")
		}
		if q1.DisplayWidth == nil || *q1.DisplayWidth != 6 {
			t.Errorf("display_width = %v, want 6", q1.DisplayWidth)
		}
		if q1.PrintFormat.Code != uint8(spsstest.FormatF) || q1.PrintFormat.Width != 8 {
			t.Errorf("print_format = %+v", q1.PrintFormat)
		}
		if q1.WriteFormat.Code == 0 {
			t.Error("write_format is unset")
		}
		if q1.Label != "Overall satisfaction" || !q1.HasLabel {
			t.Errorf("label = %q has_label = %v", q1.Label, q1.HasLabel)
		}
		if q1.Position != 0 {
			t.Errorf("position = %d, want 0", q1.Position)
		}

		// Declared string width in BYTES, which is what an export
		// re-pads to after this reader trimmed the padding off.
		region := varOf(t, doc, "REGION")
		if region.DeclaredWidth != 6 {
			t.Errorf("REGION declared_width = %d, want 6 bytes", region.DeclaredWidth)
		}
		if region.TypeCode != 6 {
			t.Errorf("REGION type_code = %d, want 6", region.TypeCode)
		}

		// A numeric declares no string width at all.
		wt := varOf(t, doc, "WT")
		if wt.DeclaredWidth != 0 {
			t.Errorf("WT declared_width = %d, want 0 for a numeric", wt.DeclaredWidth)
		}
		if wt.Measure != "scale" {
			t.Errorf("WT measure = %q, want scale", wt.Measure)
		}
		if wt.PulseType != encoding.FieldTypeF64.String() {
			t.Errorf("WT pulse_type = %q, want %s", wt.PulseType, encoding.FieldTypeF64)
		}
		if wt.Kind != "numeric" {
			t.Errorf("WT kind = %q, want numeric", wt.Kind)
		}
	})

	t.Run("every source variable is present, in cohort order", func(t *testing.T) {
		want := []string{"Satisfaction", "WT", "REGION", "MD1", "MD2"}
		if len(p.Variables) != len(want) {
			t.Fatalf("variables = %d, want %d", len(p.Variables), len(want))
		}
		for i, name := range want {
			if p.Variables[i].Name != name {
				t.Errorf("variables[%d] = %q, want %q", i, p.Variables[i].Name, name)
			}
			if p.Variables[i].Position != i {
				t.Errorf("variables[%d] position = %d", i, p.Variables[i].Position)
			}
		}
	})

	t.Run("derived registry records the multiple-dichotomy set column", func(t *testing.T) {
		// The slot E4-S1 reserved, now populated. No variable in this
		// fixture declares user-missing values, so the ONLY derived
		// column is E4-S4's `set_*` convenience column for $brands —
		// which is additive, so MD1 and MD2 are still their own
		// variables above.
		if len(p.Derived) != 1 {
			t.Fatalf("derived = %v, want exactly the $brands set column", p.Derived)
		}
		d := p.Derived[0]
		if d.Name != "brands" {
			t.Errorf("derived name = %q, want %q (the set name with its '$' dropped)", d.Name, "brands")
		}
		if d.Kind != DerivedKindMultipleDichotomy {
			t.Errorf("derived kind = %q, want %q", d.Kind, DerivedKindMultipleDichotomy)
		}
		if d.SetName != "$brands" {
			t.Errorf("derived set_name = %q, want %q — the '$' is the identity of the definition", d.SetName, "$brands")
		}
		// Bit order: bit i is Sources[i].
		if len(d.Sources) != 2 || d.Sources[0] != "MD1" || d.Sources[1] != "MD2" {
			t.Errorf("derived sources = %v, want [MD1 MD2] in bit order", d.Sources)
		}
		// Placed after the LAST constituent, which is cohort position 4.
		if d.Position != 5 {
			t.Errorf("derived position = %d, want 5 (immediately after MD2)", d.Position)
		}
		// A set column needs no Reasons dictionary: everything it shows
		// is already in its constituents.
		if len(d.Reasons) != 0 {
			t.Errorf("derived reasons = %v, want none for a set column", d.Reasons)
		}
		// The MC set contributes nothing derived — E4-S5 owns MC.
		for _, e := range p.Derived {
			if e.SetName == "$ranks" {
				t.Errorf("a multiple-CATEGORY set derived a column: %+v", e)
			}
		}
	})
}

// TestSidecar_VeryLongStringSegmentation keeps the physical layout a
// write path re-segments against.
func TestSidecar_VeryLongStringSegmentation(t *testing.T) {
	spec := spsstest.Spec{
		Vars:  []spsstest.Var{{Name: "ESSAY", Width: 600, Label: "Essay"}},
		Cases: [][]spsstest.Value{{spsstest.Text(strings.Repeat("x", 600))}},
	}
	_, _, doc := importFixture(t, spec)

	if len(doc.Payload.VeryLongStrings) != 1 {
		t.Fatalf("very_long_strings = %v, want 1 declaration", doc.Payload.VeryLongStrings)
	}
	if doc.Payload.VeryLongStrings[0].Width != 600 {
		t.Errorf("declaration width = %d, want the logical 600", doc.Payload.VeryLongStrings[0].Width)
	}

	v := varOf(t, doc, "ESSAY")
	if v.DeclaredWidth != 600 {
		t.Errorf("declared_width = %d, want the LOGICAL 600, not a segment's 255", v.DeclaredWidth)
	}
	if v.VeryLongString == nil {
		t.Fatal("very_long_string layout is nil")
	}
	if v.VeryLongString.Width != 600 {
		t.Errorf("layout width = %d, want 600", v.VeryLongString.Width)
	}
	if len(v.VeryLongString.Segments) < 2 {
		t.Fatalf("segments = %v; a one-segment very long string is a contradiction", v.VeryLongString.Segments)
	}
	content := 0
	for _, s := range v.VeryLongString.Segments {
		if s.Name == "" {
			t.Error("a segment carries no source name")
		}
		if s.Content > s.Width {
			t.Errorf("segment %q content %d exceeds its declared width %d", s.Name, s.Content, s.Width)
		}
		content += s.Content
	}
	if content != 600 {
		t.Errorf("segment contents sum to %d, want the logical width 600", content)
	}
}

// ---------------------------------------------------------------------------
// The optional-interface contract
// ---------------------------------------------------------------------------

// TestImportJob_NoSidecarEmitter_WritesNothing is the absent-path
// guarantee: a Reader that does not implement SidecarEmitter takes no
// part, so every other format's import is unchanged.
func TestImportJob_NoSidecarEmitter_WritesNothing(t *testing.T) {
	fs := afero.NewMemMapFs()
	src := &plainReader{header: []string{"a"}, rows: [][]string{{"1"}, {"2"}}}
	if _, ok := any(src).(pio.SidecarEmitter); ok {
		t.Fatal("test fixture must NOT implement SidecarEmitter")
	}
	job := pio.NewImportJob(src, "out.pulse")
	job.FS = fs
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("ImportJob.Run: %v", err)
	}
	names, err := afero.ReadDir(fs, ".")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, n := range names {
		if strings.HasSuffix(n.Name(), SidecarSuffix) {
			t.Errorf("a non-emitting source produced a sidecar: %s", n.Name())
		}
	}
}

// plainReader is a minimal pio.Reader / pio.ResetReader implementing
// none of the metadata-bearing optional interfaces. ResetReader is
// required because the shared inference pass rewinds the source; what
// matters here is that it is NOT a SidecarEmitter.
type plainReader struct {
	header []string
	rows   [][]string
}

func (r *plainReader) Reset() error { return nil }

func (r *plainReader) ReadHeader() ([]string, error) { return r.header, nil }
func (r *plainReader) ReadRows(ctx context.Context, fn func([]string) error) error {
	for _, row := range r.rows {
		if err := fn(row); err != nil {
			return err
		}
	}
	return nil
}
func (r *plainReader) Close() error { return nil }

func TestWriteSidecar_NoFilesystemIsCoded(t *testing.T) {
	r := NewReaderFromBytes(build(t, richSpec()))
	if err := r.WriteSidecar(nil, "out.pulse"); err == nil {
		t.Fatal("want an error with no filesystem")
	}
}

func TestWriteSidecar_MissingCohortIsCoded(t *testing.T) {
	// The fingerprint describes the cohort, so a cohort that is not
	// there is a fault rather than something to fingerprint as empty.
	fs := afero.NewMemMapFs()
	r := NewReaderFromBytes(build(t, richSpec()))
	if err := r.WriteSidecar(fs, "absent.pulse"); err == nil {
		t.Fatal("want an error fingerprinting an absent cohort")
	}
	if ok, _ := afero.Exists(fs, SidecarPath("absent.pulse")); ok {
		t.Error("a failed fingerprint still wrote a sidecar")
	}
}

// ---------------------------------------------------------------------------
// JSON-safe floats
// ---------------------------------------------------------------------------

// TestFloat_NonFiniteSurvivesJSON exists because encoding/json REFUSES
// to marshal NaN and +/-Inf: one pathological double anywhere in a
// dictionary would otherwise fail the whole sidecar write, and with it
// an import that is perfectly good.
func TestFloat_NonFiniteSurvivesJSON(t *testing.T) {
	tests := []struct {
		name string
		in   float64
		want string
		is   func(float64) bool
	}{
		{"finite", 1.5, "1.5", func(v float64) bool { return v == 1.5 }},
		{"negative zero", math.Copysign(0, -1), "-0", func(v float64) bool { return math.Signbit(v) }},
		{"NaN", math.NaN(), `"NaN"`, math.IsNaN},
		{"+Inf", math.Inf(1), `"Infinity"`, func(v float64) bool { return math.IsInf(v, 1) }},
		{"-Inf", math.Inf(-1), `"-Infinity"`, func(v float64) bool { return math.IsInf(v, -1) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := stdjson.Marshal(Float(tc.in))
			if err != nil {
				t.Fatalf("Marshal(%v): %v", tc.in, err)
			}
			if string(raw) != tc.want {
				t.Errorf("Marshal(%v) = %s, want %s", tc.in, raw, tc.want)
			}
			var back Float
			if err := stdjson.Unmarshal(raw, &back); err != nil {
				t.Fatalf("Unmarshal(%s): %v", raw, err)
			}
			if !tc.is(float64(back)) {
				t.Errorf("round trip of %v yielded %v", tc.in, float64(back))
			}
		})
	}
}

func TestFloat_UnknownTokenIsRejected(t *testing.T) {
	var f Float
	if err := stdjson.Unmarshal([]byte(`"inf"`), &f); err == nil {
		t.Fatal("want an error for an unrecognised non-finite token")
	}
}

// TestSidecar_NonFiniteCategoryCodeDoesNotFailTheImport is the same
// hazard end to end: a data value the JSON number grammar cannot
// express must not take the import down with it.
func TestSidecar_NonFiniteCategoryCodeDoesNotFailTheImport(t *testing.T) {
	spec := spsstest.Spec{
		Vars: []spsstest.Var{{Name: "ODD", Measure: spsstest.MeasureNominal}},
		Cases: [][]spsstest.Value{
			{spsstest.Num(1)},
			{spsstest.Num(math.Inf(1))},
		},
		ValueLabels: []spsstest.ValueLabelSet{{
			Vars:   []string{"ODD"},
			Labels: []spsstest.ValueLabel{{Value: spsstest.Num(1), Label: "one"}},
		}},
	}
	_, _, doc := importFixture(t, spec)
	v := varOf(t, doc, "ODD")
	if len(v.Categories) == 0 {
		t.Fatal("ODD carries no categories")
	}
	// The canonical Value text is the authoritative record for a
	// category; Code is the convenience that must not be able to fail
	// the write.
	for _, c := range v.Categories {
		if c.Value == "" {
			t.Error("a category carries no dictionary text")
		}
	}
}

// ---------------------------------------------------------------------------
// Round trip
// ---------------------------------------------------------------------------

// TestSidecar_RoundTripsThroughJSON proves the document decodes back to
// the value that was written — the read path E5-S1 builds on.
func TestSidecar_RoundTripsThroughJSON(t *testing.T) {
	fs, cohort, decoded := importFixture(t, richSpec())

	raw, err := afero.ReadFile(fs, SidecarPath(cohort))
	if err != nil {
		t.Fatalf("reading sidecar: %v", err)
	}
	again, err := stdjson.MarshalIndent(decoded, "", "  ")
	if err != nil {
		t.Fatalf("re-marshalling: %v", err)
	}
	if strings.TrimSpace(string(raw)) != strings.TrimSpace(string(again)) {
		t.Error("decode -> encode is not byte-stable")
	}
}

// TestSidecar_IsDeterministic keeps the document diffable: the same
// source must produce the same payload every time, or a review cannot
// see a fidelity regression.
func TestSidecar_IsDeterministic(t *testing.T) {
	spec := richSpec()
	_, _, a := importFixture(t, spec)
	_, _, b := importFixture(t, spec)

	pa, err := stdjson.Marshal(a.Payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	pb, err := stdjson.Marshal(b.Payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(pa) != string(pb) {
		t.Error("two imports of the same source produced different payloads")
	}
}
