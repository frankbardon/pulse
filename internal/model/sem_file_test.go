package model

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// sem_file_test.go pins the SEM fit file (U59 E3-S3): a SEMFit encodes
// to one byte sequence and decodes back to the same fit with every float
// bit kept (NaN, ±Inf, -0, subnormals included); the decode is
// version-first and strict; a `models` entry's .pulse-sem.json suffix
// dispatches the kind — LoadFiles keeps it beside the model files,
// WithLoaded binds it as a SEM fit and CatalogWithLoaded signs it kind
// sem; CheckLoadedSEM refuses a file whose model does not rebuild.

// semOrdinalFixture is semFileFixture as an ordinal (WLSMV) fit: a and
// c ordered, b numeric.
func semOrdinalFixture() *SEMFit {
	f := semFileFixture()
	f.Estimator, f.Ordered = "wlsmv", []string{"a", "c"}
	f.Categories = [][]float64{{1, 2, 3}, {0, 1}}
	for g := range f.Groups {
		f.Groups[g].Thresholds = [][]float64{{-0.7, 0.4 + float64(g)}, {0.12345678901234567}}
	}
	return f
}

// semFileFixture is a two-group fit whose figures exercise the Float
// policy.
func semFileFixture() *SEMFit {
	grp := func(level string, k float64) SEMGroup {
		return SEMGroup{
			Level: level, N: 600, SumWeights: 600,
			Lambda:     [][]float64{{1, 0}, {0.8 * k, 0}, {0, 1}},
			Beta:       [][]float64{{0, 0}, {0.4, 0}},
			Psi:        [][]float64{{0.31234567890123456, 0}, {0, math.Copysign(0, -1)}},
			Theta:      [][]float64{{0.2, 0, 0}, {0, 5e-324, 0}, {0, 0, 1.0 / 3}},
			Nu:         []float64{3.1, 2.9, math.Inf(1)},
			Alpha:      []float64{0, math.Inf(-1)},
			SampleMean: []float64{3.1, math.NaN(), 2.7},
		}
	}
	return &SEMFit{
		Name: "brand", Operator: "MAT_SEM", Model: "f =~ a + b\ng =~ c\ng ~ f",
		Labels: []string{"a", "b", "c"}, Members: []string{"q_a", "q_b", "q_c"},
		Observed: []string{"a", "b", "c"}, Inner: []string{"f", "g"}, Latents: []string{"f", "g"},
		Estimator: "ml", Lavaan: true, MeanStructure: true,
		Group: "wave", GroupReference: "1", GroupEqual: []string{"loadings"},
		Solver: "fisher", Information: "dense", Basis: VectorBasisFrequency,
		N: 1200, SumWeights: 1200.5, DoF: 7, NParams: 22, Converged: true, Status: "converged", Iterations: 31,
		NearestPD: true, MinEigenvalue: -1e-17,
		Groups: []SEMGroup{grp("1", 1), grp("2", 1.25)},
	}
}

// sameSEMFit compares two fits with every float by its bits (a NaN
// equal to a NaN).
func sameSEMFit(t *testing.T, got, want *SEMFit) {
	t.Helper()
	bits := func(v float64) uint64 {
		if math.IsNaN(v) {
			return math.Float64bits(math.NaN())
		}
		return math.Float64bits(v)
	}
	var walk func(path string, g, w reflect.Value)
	walk = func(path string, g, w reflect.Value) {
		switch g.Kind() {
		case reflect.Float64:
			if bits(g.Float()) != bits(w.Float()) {
				t.Errorf("%s: %v (%#x), want %v (%#x)", path, g.Float(), bits(g.Float()), w.Float(), bits(w.Float()))
			}
		case reflect.Slice:
			if g.Len() != w.Len() {
				t.Errorf("%s: %d entries, want %d", path, g.Len(), w.Len())
				return
			}
			for i := 0; i < g.Len(); i++ {
				walk(path+"[]", g.Index(i), w.Index(i))
			}
		case reflect.Struct:
			for i := 0; i < g.NumField(); i++ {
				walk(path+"."+g.Type().Field(i).Name, g.Field(i), w.Field(i))
			}
		default:
			if !reflect.DeepEqual(g.Interface(), w.Interface()) {
				t.Errorf("%s: %v, want %v", path, g.Interface(), w.Interface())
			}
		}
	}
	walk("SEMFit", reflect.ValueOf(*got), reflect.ValueOf(*want))
}

func encodedSEM(t *testing.T, f *SEMFit) []byte {
	t.Helper()
	sf, err := NewSEMFile(f.Name, f, Provenance{Cohort: "brand.pulse", RequestDigest: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := sf.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSEMFile_RoundTripBitExact(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		fixture func() *SEMFit
		version string
	}{{semFileFixture, `"sem_format_version": 1`}, {semOrdinalFixture, `"sem_format_version": 2`}} {
		want := c.fixture()
		data := encodedSEM(t, want)
		if !bytes.Contains(data, []byte(c.version)) {
			t.Fatalf("want %s:\n%s", c.version, data)
		}
		if want.Ordinal() != bytes.Contains(data, []byte(`"thresholds"`)) {
			t.Fatalf("ordinal %v, thresholds in the document %v", want.Ordinal(), !want.Ordinal())
		}
		file, err := DecodeSEMFile("x"+SEMSuffix, data)
		if err != nil {
			t.Fatal(err)
		}
		sameSEMFit(t, file.SEMFit(want.Name), want)
		if back := encodedSEM(t, file.SEMFit(want.Name)); !bytes.Equal(back, data) {
			t.Fatal("re-encode differs")
		}
	}
	for _, mutate := range []func(*SEMFit){
		func(*SEMFit) {},
		// Ungrouped, covariance-only, semopy conventions, no nearest PD.
		func(f *SEMFit) {
			f.Group, f.GroupReference, f.GroupEqual, f.Lavaan, f.MeanStructure, f.NearestPD, f.MinEigenvalue = "", "", nil, false, false, false, 0
			f.Groups = f.Groups[:1]
			f.Groups[0].Level = ""
			f.Basis = VectorBasisUnweighted
		},
	} {
		want := semFileFixture()
		mutate(want)
		data := encodedSEM(t, want)
		if again := encodedSEM(t, want); !bytes.Equal(again, data) {
			t.Fatal("one fit encodes to two byte sequences")
		}
		if !bytes.HasSuffix(data, []byte("\n")) || !bytes.Contains(data, []byte(`"sem_format_version": 1`)) {
			t.Fatalf("encoding %s", data)
		}
		file, err := DecodeSEMFile("x"+SEMSuffix, data)
		if err != nil {
			t.Fatal(err)
		}
		got := file.SEMFit(want.Name)
		sameSEMFit(t, got, want)
		if file.Provenance.Cohort != "brand.pulse" || file.Name != "brand" {
			t.Fatalf("provenance %+v name %q", file.Provenance, file.Name)
		}
		// Re-encoding the decoded fit gives the same bytes.
		if back := encodedSEM(t, got); !bytes.Equal(back, data) {
			t.Fatalf("re-encode differs\n%s\n%s", back, data)
		}
	}
	// The Float policy is visible in the document.
	data := encodedSEM(t, semFileFixture())
	for _, frag := range []string{`"+Inf"`, `"-Inf"`, `null`, `-0`, `5e-324`} {
		if !bytes.Contains(data, []byte(frag)) {
			t.Errorf("document lacks %s", frag)
		}
	}
}

func TestSEMFile_DecodeRefusals(t *testing.T) {
	t.Parallel()

	base := func() map[string]any {
		var m map[string]any
		if err := json.Unmarshal(encodedSEM(t, semFileFixture()), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	group := func(m map[string]any, g int) map[string]any { return m["groups"].([]any)[g].(map[string]any) }
	// derive makes m a version-3 fit grouped by a computed column,
	// declared levels levels (nil: its integer levels).
	derive := func(m map[string]any, levels []any) {
		m["sem_format_version"], m["group_derived"] = 3, true
		if levels != nil {
			m["group_levels"] = levels
		}
	}
	level := func(v float64, l string) map[string]any { return map[string]any{"value": v, "label": l} }
	for _, ok := range []struct {
		name   string
		levels []any
	}{{"integer levels", nil}, {"declared levels", []any{level(2, "2"), level(1, "1")}}} {
		m := base()
		derive(m, ok.levels)
		data, _ := json.Marshal(m)
		f, err := DecodeSEMFile("f"+SEMSuffix, data)
		if err != nil {
			t.Fatalf("derived %s: %v", ok.name, err)
		}
		sf := f.SEMFit("x")
		if !sf.GroupDerived || len(sf.GroupLevels) != len(ok.levels) || !sf.Signature().SEMGroupDerived {
			t.Fatalf("derived %s: fit %+v", ok.name, sf)
		}
		again, err := NewSEMFile("x", sf, Provenance{})
		if err != nil || again.SEMFormatVersion != SEMFormatVersionDerivedGroup {
			t.Fatalf("derived %s: re-save %v version %v", ok.name, err, again)
		}
	}
	for _, c := range []struct {
		name   string
		mutate func(map[string]any)
		code   errors.Code
		reason string
	}{
		{"version missing", func(m map[string]any) { delete(m, "sem_format_version") }, errors.PULSE_MODEL_FILE_INVALID, ReasonFileVersionMissing},
		{"version null", func(m map[string]any) { m["sem_format_version"] = nil }, errors.PULSE_MODEL_FILE_INVALID, ReasonFileVersionMissing},
		{"version string", func(m map[string]any) { m["sem_format_version"] = "1" }, errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"version 4", func(m map[string]any) { m["sem_format_version"] = 4 }, errors.PULSE_MODEL_VERSION_UNSUPPORTED, ""},
		{"version 2 without ordered", func(m map[string]any) { m["sem_format_version"] = 2 }, errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"ordered under version 1", func(m map[string]any) { m["ordered"] = []any{"a"} }, errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"thresholds under version 1", func(m map[string]any) { group(m, 0)["thresholds"] = []any{[]any{0}} }, errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"unknown key", func(m map[string]any) { m["extra"] = 1 }, errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"wrong type", func(m map[string]any) { m["n"] = "x" }, errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"no model", func(m map[string]any) { m["model"] = " " }, errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"no labels", func(m map[string]any) { m["labels"] = []any{} }, errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"repeated label", func(m map[string]any) { m["labels"] = []any{"a", "a", "c"} }, errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"members unlike labels", func(m map[string]any) { m["members"] = []any{"x"} }, errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"latent outside inner", func(m map[string]any) { m["latents"] = []any{"z"} }, errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"conventions", func(m map[string]any) { m["conventions"] = "mplus" }, errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"probability basis", func(m map[string]any) { m["basis"] = "probability" }, errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"negative n", func(m map[string]any) { m["n"] = -1 }, errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"infinite weight", func(m map[string]any) { m["sum_weights"] = "+Inf" }, errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"no groups", func(m map[string]any) { m["groups"] = []any{} }, errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"ungrouped two groups", func(m map[string]any) { delete(m, "group") }, errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"repeated level", func(m map[string]any) { group(m, 1)["level"] = "1" }, errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"lambda rows", func(m map[string]any) { group(m, 0)["lambda"] = []any{[]any{1, 0}} }, errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"theta row", func(m map[string]any) { group(m, 1)["theta"].([]any)[2] = []any{0} }, errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"alpha length", func(m map[string]any) { group(m, 0)["alpha"] = []any{0} }, errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"bad float string", func(m map[string]any) { group(m, 0)["nu"].([]any)[0] = "Inf" }, errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"version 3 not derived", func(m map[string]any) { m["sem_format_version"] = 3 }, errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"group_derived under version 1", func(m map[string]any) { m["group_derived"] = true }, errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"derived ungrouped", func(m map[string]any) { derive(m, nil); delete(m, "group") }, errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"derived level not an integer", func(m map[string]any) { derive(m, nil); group(m, 0)["level"] = "a" }, errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"derived level not canonical", func(m map[string]any) { derive(m, nil); group(m, 0)["level"] = "01" }, errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"one declared level", func(m map[string]any) { derive(m, []any{level(1, "1")}) }, errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"declared value not an integer", func(m map[string]any) { derive(m, []any{level(1.5, "1"), level(2, "2")}) }, errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"declared value twice", func(m map[string]any) { derive(m, []any{level(1, "1"), level(1, "2")}) }, errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"declared label twice", func(m map[string]any) { derive(m, []any{level(1, "1"), level(2, "1")}) }, errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
		{"group level undeclared", func(m map[string]any) { derive(m, []any{level(1, "1"), level(3, "3")}) }, errors.PULSE_MODEL_FILE_INVALID, ReasonFileSchema},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := base()
			c.mutate(m)
			data, _ := json.Marshal(m)
			_, err := DecodeSEMFile("f"+SEMSuffix, data)
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != c.code || (c.reason != "" && ce.Details["reason"] != c.reason) || ce.Details["path"] != "f"+SEMSuffix {
				t.Fatalf("got %v, want %s %s", err, c.code, c.reason)
			}
		})
	}
	if _, err := DecodeSEMFile("f", []byte("[")); err == nil {
		t.Fatal("malformed JSON decoded")
	}
	ordinal := func() map[string]any {
		var m map[string]any
		if err := json.Unmarshal(encodedSEM(t, semOrdinalFixture()), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	for _, c := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"ordered outside observed", func(m map[string]any) { m["ordered"] = []any{"a", "z"} }},
		{"ordered out of order", func(m map[string]any) { m["ordered"] = []any{"c", "a"} }},
		{"repeated ordered", func(m map[string]any) { m["ordered"] = []any{"a", "a"} }},
		{"category lists", func(m map[string]any) { m["categories"] = []any{[]any{1, 2}} }},
		{"one category", func(m map[string]any) { m["categories"].([]any)[1] = []any{0} }},
		{"categories descending", func(m map[string]any) { m["categories"].([]any)[0] = []any{3, 2, 1} }},
		{"threshold lists", func(m map[string]any) { group(m, 1)["thresholds"] = []any{[]any{0, 1}} }},
		{"threshold count", func(m map[string]any) { group(m, 0)["thresholds"].([]any)[0] = []any{0} }},
		{"infinite threshold", func(m map[string]any) { group(m, 0)["thresholds"].([]any)[1] = []any{"+Inf"} }},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := ordinal()
			c.mutate(m)
			data, _ := json.Marshal(m)
			_, err := DecodeSEMFile("f"+SEMSuffix, data)
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_MODEL_FILE_INVALID || ce.Details["reason"] != ReasonFileSchema {
				t.Fatalf("got %v, want PULSE_MODEL_FILE_INVALID schema", err)
			}
		})
	}
	// The writer refuses what the decode refuses.
	bad := semFileFixture()
	bad.Groups[0].Nu = bad.Groups[0].Nu[:1]
	if _, err := NewSEMFile("x", bad, Provenance{}); err == nil || err.(*errors.CodedError).Code != errors.PROCESSING_INTERNAL {
		t.Fatalf("a malformed fit saved: %v", err)
	}
	if _, err := NewSEMFile("x", nil, Provenance{}); err == nil {
		t.Fatal("a nil fit saved")
	}
}

// TestSEMFile_ModelsEntryDispatch: a `models` entry's suffix picks the
// kind — .pulse-sem.json loads a SEM fit file (bound as a SEM fit,
// signed kind sem, its source the entry), .pulse-model.json a model
// file; any other suffix is refused naming both, and the SEM path rule
// (stem, root) holds.
func TestSEMFile_ModelsEntryDispatch(t *testing.T) {
	t.Parallel()

	sem := "fits/brand" + SEMSuffix
	files := map[string][]byte{
		sem:                 encodedSEM(t, semFileFixture()),
		"m" + FileSuffix:    encodedFullFit(t),
		"v9" + SEMSuffix:    []byte(`{"sem_format_version": 9}`),
		"junk" + SEMSuffix:  []byte(`{`),
		"model" + SEMSuffix: encodedFullFit(t),
	}
	rs := refs("m", "m"+FileSuffix, "s", sem)
	if cerr := CheckRefs(Refs{Models: rs}, nil); cerr != nil {
		t.Fatal(cerr)
	}
	loaded, cerr := LoadFiles(Refs{Models: rs}, memRead(files))
	if cerr != nil {
		t.Fatal(cerr)
	}
	if len(loaded.Models) != 2 || loaded.Models[0] == nil || loaded.Models[1] != nil || len(loaded.SEMs) != 2 || loaded.SEMs[0] != nil || loaded.SEMs[1] == nil {
		t.Fatalf("loaded %+v", loaded)
	}
	if only, cerr := LoadModelFiles(rs, memRead(files)); cerr != nil || len(only) != 2 || only[1] != nil {
		t.Fatalf("LoadModelFiles: %v %v", only, cerr)
	}
	set, err := (*Set)(nil).WithLoaded(Refs{Models: rs}, loaded)
	if err != nil {
		t.Fatal(err)
	}
	got, err := set.LookupSEM("s")
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != ModelSource(1, sem) || got.Name != "s" {
		t.Fatalf("bound %+v", got.Source)
	}
	want := semFileFixture()
	want.Name, want.Source = "s", ModelSource(1, sem)
	sameSEMFit(t, got, want)
	if _, err := set.Lookup("m"); err != nil {
		t.Fatalf("the model file beside it: %v", err)
	}
	if _, err := set.LookupVector("s"); err == nil {
		t.Fatal("a loaded SEM fit reads as a vector fit")
	}
	cat := CatalogWithLoaded(nil, Refs{Models: rs}, loaded)
	if sig := cat["s"]; sig.Kind != types.FitKindSEM || !reflect.DeepEqual(sig.Members, []string{"a", "b", "c"}) || sig.Source != ModelSource(1, sem) {
		t.Fatalf("catalog %+v", sig)
	}
	if sig := cat["m"]; sig.EffectiveKind() != types.FitKindRegression {
		t.Fatalf("catalog %+v", sig)
	}
	if cerr := CheckSEMFit(cat, "s"); cerr != nil {
		t.Fatal(cerr)
	}

	// Load faults name the entry.
	for _, c := range []struct {
		path string
		code errors.Code
	}{
		{"v9" + SEMSuffix, errors.PULSE_MODEL_VERSION_UNSUPPORTED},
		{"junk" + SEMSuffix, errors.PULSE_MODEL_FILE_INVALID},
		{"model" + SEMSuffix, errors.PULSE_MODEL_FILE_INVALID}, // a model file under the SEM suffix
		{"absent" + SEMSuffix, errors.PULSE_MODEL_FILE_UNREADABLE},
	} {
		_, cerr := LoadFiles(Refs{Models: refs("m", "m"+FileSuffix, "s", c.path)}, memRead(files))
		if cerr == nil || cerr.Code != c.code || cerr.Details["model"] != 1 || cerr.Details["name"] != "s" {
			t.Fatalf("%s: %v, want %s at models[1]", c.path, cerr, c.code)
		}
	}
	// A SEM file under the model suffix is the model file's refusal.
	files["sem"+FileSuffix] = files[sem]
	if _, cerr := LoadFiles(Refs{Models: refs("s", "sem"+FileSuffix)}, memRead(files)); cerr == nil || cerr.Code != errors.PULSE_MODEL_FILE_INVALID {
		t.Fatalf("a SEM file under .pulse-model.json: %v", cerr)
	}

	// The path rule.
	for _, c := range []struct{ path, reason string }{
		{"s" + PopulationSuffix, ReasonSaveSuffix},
		{"s.json", ReasonSaveSuffix},
		{SEMSuffix, ReasonSaveNameMissing},
		{"../s" + SEMSuffix, ReasonSaveEscapesRoot},
		{"a/../../s" + SEMSuffix, ReasonSaveEscapesRoot},
	} {
		cerr := CheckRefs(Refs{Models: refs("s", c.path)}, nil)
		if cerr == nil || cerr.Code != errors.PULSE_MODEL_REF_INVALID || cerr.Details["reason"] != c.reason {
			t.Fatalf("%s: %v, want reason %s", c.path, cerr, c.reason)
		}
		if c.reason == ReasonSaveSuffix && !strings.Contains(cerr.Message, SEMSuffix) {
			t.Fatalf("suffix refusal does not name %s: %s", SEMSuffix, cerr.Message)
		}
	}
	// One namespace: a loaded SEM fit's name collides with a fit's.
	fits := CatalogFromFits([]types.FitSpec{{Name: "s", Request: semFitReq()}})
	if cerr := CheckRefs(Refs{Models: refs("s", sem)}, fits); cerr == nil || cerr.Code != errors.PULSE_MODEL_NAME_COLLISION {
		t.Fatalf("collision: %v", cerr)
	}
}

func TestSEMFile_CheckLoadedSEM(t *testing.T) {
	t.Parallel()

	sem := "s" + SEMSuffix
	rs := refs("m", "m"+FileSuffix, "s", sem)
	loaded, cerr := LoadFiles(Refs{Models: rs}, memRead(map[string][]byte{"m" + FileSuffix: encodedFullFit(t), sem: encodedSEM(t, semFileFixture())}))
	if cerr != nil {
		t.Fatal(cerr)
	}
	var seen *SEMFit
	ok := func(f *SEMFit) error { seen = f; return nil }
	if cerr := CheckLoadedSEM(Refs{Models: rs}, loaded, ok); cerr != nil || seen == nil || seen.Name != "s" || seen.Model != semFileFixture().Model {
		t.Fatalf("passing rebuild: %v %+v", cerr, seen)
	}
	fail := func(*SEMFit) error { return stderrors.New("observed differ") }
	cerr = CheckLoadedSEM(Refs{Models: rs}, loaded, fail)
	if cerr == nil || cerr.Code != errors.PULSE_MODEL_FILE_INVALID || cerr.Details["model"] != 1 || cerr.Details["name"] != "s" ||
		cerr.Details["path"] != sem || cerr.Details["reason"] != ReasonFileSchema || !strings.Contains(cerr.Message, "observed differ") {
		t.Fatalf("failing rebuild: %v", cerr)
	}
	if cerr := CheckLoadedSEM(Refs{}, nil, fail); cerr != nil {
		t.Fatal(cerr)
	}
}
