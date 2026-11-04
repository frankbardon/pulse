package pulse

import (
	"bytes"
	"context"
	stderrors "errors"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/synth"
)

// synthLookupSpec writes rate = lookup("rates", region).
func synthLookupSpec() *SynthSpec {
	return &SynthSpec{
		RowCount: 60,
		Fields: []synth.FieldSpec{
			{Name: "region", Type: "categorical_u8", Distribution: synth.DistWeightedCategorical,
				Params: map[string]any{"values": []any{"east", "west"}}},
			{Name: "rate", Type: "f64", Distribution: synth.DistUniform, Params: map[string]any{"min": 0.0, "max": 1.0}},
		},
		Rules: []synth.RuleSpec{{SetExpr: map[string]string{"rate": `lookup("rates", region)`}}},
	}
}

func synthLookupHost(t *testing.T, ext Extensions, dir string, rec *hookRecorder) *Pulse {
	t.Helper()
	opts := Options{FS: afero.NewMemMapFs(), Extensions: ext, LookupTablesDir: dir}
	if rec != nil {
		opts.Hooks = rec.hooks()
	}
	p, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func readSynthOutput(t *testing.T, p *Pulse, path string) []byte {
	t.Helper()
	b, err := afero.ReadFile(p.fsys, path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestSynth_ReadsInstanceLookupTables: Pulse.Synth with opts.LookupTables
// nil reads the instance's tables — programmatic and LookupTablesDir
// alike, the same file as the explicit map gives — and the operation's
// request hash is Spec.HashWithLookups over them.
func TestSynth_ReadsInstanceLookupTables(t *testing.T) {
	t.Parallel()

	rows := map[string]float64{"east": 0.25, "west": 0.75}
	explicit := map[string]synth.LookupTable{"rates": {Rows: rows}}
	spec := synthLookupSpec()
	want, _, err := synth.SynthBytes(spec, SynthOptions{Seed: 5, LookupTables: explicit})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeLookupFile(t, dir, "rates.json", `{"east": 0.25, "west": 0.75}`)
	for name, p := range map[string]*Pulse{
		"prog": synthLookupHost(t, Extensions{LookupTables: map[string]LookupTable{"rates": {Rows: rows}}}, "", nil),
		"dir":  synthLookupHost(t, Extensions{}, dir, nil),
	} {
		if _, err := p.Synth(context.Background(), spec, "/out.pulse", SynthOptions{Seed: 5}); err != nil {
			t.Fatalf("%s: Synth: %v", name, err)
		}
		if !bytes.Equal(readSynthOutput(t, p, "/out.pulse"), want) {
			t.Fatalf("%s: instance tables gave different bytes than the explicit map", name)
		}
	}

	// The operation identity folds the table digest: a changed cell
	// moves the request hash.
	hashFor := func(cell float64) string {
		rec := &hookRecorder{}
		p := synthLookupHost(t, Extensions{LookupTables: map[string]LookupTable{"rates": {Rows: map[string]float64{"east": cell, "west": 0.75}}}}, "", rec)
		if _, err := p.Synth(context.Background(), spec, "/out.pulse", SynthOptions{Seed: 5}); err != nil {
			t.Fatal(err)
		}
		return rec.take()[0].info.RequestHash
	}
	h1, h2 := hashFor(0.25), hashFor(0.3)
	if h1 == h2 {
		t.Fatal("a changed instance table left the synth request hash unchanged")
	}
	if h1 != spec.HashWithLookups(explicit) || h1 == spec.Hash() {
		t.Fatalf("request hash = %s, want HashWithLookups %s (table-blind Hash %s)", h1, spec.HashWithLookups(explicit), spec.Hash())
	}
}

// TestSynth_ExplicitLookupTablesWin: a non-nil opts.LookupTables is used
// as given — an empty map reads no table even on an instance that has
// one.
func TestSynth_ExplicitLookupTablesWin(t *testing.T) {
	t.Parallel()

	p := synthLookupHost(t, Extensions{LookupTables: map[string]LookupTable{"rates": {Rows: map[string]float64{"east": 1, "west": 2}}}}, "", nil)
	_, err := p.Synth(context.Background(), synthLookupSpec(), "/out.pulse", SynthOptions{Seed: 1, LookupTables: map[string]synth.LookupTable{}})
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_LOOKUP_TABLE_UNKNOWN {
		t.Fatalf("empty explicit map: err = %v, want PULSE_LOOKUP_TABLE_UNKNOWN", err)
	}
}

// TestSynth_CallbackLookupTableRefused: a spec naming a callback-backed
// instance table is refused before anything is written (its content has
// no digest); an unnamed callback table is ignored.
func TestSynth_CallbackLookupTableRefused(t *testing.T) {
	t.Parallel()

	cb := LookupTable{Lookup: func(keys ...string) (float64, bool, error) { return 1, true, nil }}
	p := synthLookupHost(t, Extensions{LookupTables: map[string]LookupTable{"rates": cb}}, "", nil)
	_, err := p.Synth(context.Background(), synthLookupSpec(), "/out.pulse", SynthOptions{Seed: 1})
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_SYNTH_EXPR_NONDETERMINISTIC || ce.Details["table"] != "rates" {
		t.Fatalf("callback table: err = %v, want PULSE_SYNTH_EXPR_NONDETERMINISTIC naming rates", err)
	}
	if ok, _ := afero.Exists(p.fsys, "/out.pulse"); ok {
		t.Fatal("a refused run wrote its output")
	}
	if _, err := p.SynthStream(context.Background(), synthLookupSpec(), SynthOptions{Seed: 1}); codeOfErr(err) != errors.PULSE_SYNTH_EXPR_NONDETERMINISTIC {
		t.Fatalf("SynthStream callback table: err = %v", err)
	}

	p2 := synthLookupHost(t, Extensions{LookupTables: map[string]LookupTable{
		"other": cb, "rates": {Rows: map[string]float64{"east": 1, "west": 2}},
	}}, "", nil)
	if _, err := p2.Synth(context.Background(), synthLookupSpec(), "/out.pulse", SynthOptions{Seed: 1}); err != nil {
		t.Fatalf("an unnamed callback table blocked the run: %v", err)
	}
}

// TestSynthStream_HeaderHashFoldsLookups: the stream header's request
// hash is the same lookup-folded identity, and the rows read the table.
func TestSynthStream_HeaderHashFoldsLookups(t *testing.T) {
	t.Parallel()

	rows := map[string]float64{"east": 0.25, "west": 0.75}
	p := synthLookupHost(t, Extensions{LookupTables: map[string]LookupTable{"rates": {Rows: rows}}}, "", nil)
	spec := synthLookupSpec()
	res, err := p.SynthStream(context.Background(), spec, SynthOptions{Seed: 2})
	if err != nil {
		t.Fatal(err)
	}
	if want := spec.HashWithLookups(map[string]synth.LookupTable{"rates": {Rows: rows}}); res.Header.RequestHash != want {
		t.Fatalf("header hash = %s, want %s", res.Header.RequestHash, want)
	}
	n := 0
	for c := range res.Chunks {
		if r := c.Data["rate"]; r != 0.25 && r != 0.75 {
			t.Fatalf("row %d: rate = %v, not a table cell", n, r)
		}
		n++
	}
	if term := <-res.Done; term.Error != nil || n != spec.RowCount {
		t.Fatalf("stream ended %v after %d rows", term.Error, n)
	}
}

func codeOfErr(err error) errors.Code {
	var ce *errors.CodedError
	if stderrors.As(err, &ce) {
		return ce.Code
	}
	return ""
}
