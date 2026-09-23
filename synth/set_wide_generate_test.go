package synth_test

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/synth"
	"github.com/spf13/afero"
)

// wideSetOptionNames returns n deterministically-named options. The
// names are zero-padded so the declaration order (which IS the bit
// order — see buildSchema's dictionary pre-registration) is readable in
// a failure message.
func wideSetOptionNames(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("opt%03d", i)
	}
	return out
}

// wideSetParams builds the set_bernoulli params for n options whose
// per-option frequency is base, overridden at the indices in probe.
func wideSetParams(n int, base float64, probe map[int]float64) (map[string]any, []string, []float64) {
	names := wideSetOptionNames(n)
	freqs := make([]float64, n)
	for i := range freqs {
		freqs[i] = base
	}
	for i, p := range probe {
		freqs[i] = p
	}
	optionsAny := make([]any, n)
	freqsAny := make([]any, n)
	for i := range names {
		optionsAny[i] = names[i]
		freqsAny[i] = freqs[i]
	}
	return map[string]any{"options": optionsAny, "frequencies": freqsAny}, names, freqs
}

// readWideSetFieldRows decodes a set_* field of ANY rung into per-row
// SetMask plus null flag, through the ordinary RecordReader path. It
// deliberately does NOT assert on the uint64 wide-map form the narrow
// rungs use: a wide rung lands in the wide map as an encoding.SetMask
// and a helper that type-asserted uint64 would read every wide row as
// an empty selection — exactly the silent failure this story guards.
func readWideSetFieldRows(t *testing.T, data []byte, name string) (masks []encoding.SetMask, nullFlags []bool, dict *encoding.Dictionary) {
	t.Helper()
	r := bytes.NewReader(data)
	if err := encoding.ReadHeader(r); err != nil {
		t.Fatalf("read header: %v", err)
	}
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	f := schema.Field(name)
	if f == nil || !f.Type.IsSet() || f.Dictionary == nil {
		t.Fatalf("not a set field: %s", name)
	}
	rr := encoding.NewRecordReader(r, schema)
	values := make(map[string]float64)
	nulls := make(map[string]bool)
	wide := make(map[string]any)
	for {
		if err := rr.ReadRecordWithWide(values, nulls, wide); err != nil {
			break
		}
		var m encoding.SetMask
		switch v := wide[name].(type) {
		case encoding.SetMask:
			m = v
		case uint64:
			m = encoding.SetMaskFromUint64(v)
		default:
			// A null row has its wide entry deleted by the decoder
			// (encoding/reader.go); anything else is a shape the
			// generator should never have produced.
			if !nulls[name] {
				t.Fatalf("field %q: wide value is %T, want encoding.SetMask or uint64",
					name, wide[name])
			}
		}
		masks = append(masks, m)
		nullFlags = append(nullFlags, nulls[name])
	}
	return masks, nullFlags, f.Dictionary
}

// TestSynth_SetBernoulli_WideRungMarginalFrequencies is the story's
// central acceptance bar: per-element selection frequency must match
// the declared Bernoulli probability for members BELOW and ABOVE bit
// 64. A generator that quietly drops the high words still produces a
// valid-looking cohort — every high-bit option would simply read as
// never selected — so the probes straddle the word boundary at 63/64
// and reach the top rung's last bit.
func TestSynth_SetBernoulli_WideRungMarginalFrequencies(t *testing.T) {
	const rowCount = 10000
	const tolerance = 0.03

	cases := []struct {
		typeName string
		options  int
		probe    map[int]float64
	}{
		{
			typeName: "set_u128",
			options:  128,
			probe: map[int]float64{
				0: 0.9, 3: 0.7, 63: 0.25, 64: 0.8, 65: 0.1, 127: 0.6,
			},
		},
		{
			typeName: "set_u256",
			options:  256,
			probe: map[int]float64{
				1: 0.85, 63: 0.2, 64: 0.75, 130: 0.35, 200: 0.65, 255: 0.45,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.typeName, func(t *testing.T) {
			params, names, freqs := wideSetParams(tc.options, 0.5, tc.probe)
			spec := &synth.Spec{
				RowCount: rowCount,
				Fields: []synth.FieldSpec{
					{Name: "features", Type: tc.typeName,
						Distribution: synth.DistSetBernoulli, Params: params},
				},
			}
			data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 7})
			if err != nil {
				t.Fatalf("SynthBytes(%s): %v", tc.typeName, err)
			}

			masks, nullFlags, dict := readWideSetFieldRows(t, data, "features")
			if len(masks) != rowCount {
				t.Fatalf("row count = %d, want %d", len(masks), rowCount)
			}
			if dict.Count() != tc.options {
				t.Fatalf("dictionary size = %d, want %d", dict.Count(), tc.options)
			}

			counts := make([]int, tc.options)
			highBitSeen := false
			for i, m := range masks {
				if nullFlags[i] {
					t.Fatalf("row %d is null in a non-nullable spec", i)
				}
				if hb := m.HighestBit(); hb >= tc.options {
					t.Fatalf("row %d: mask has bit %d beyond declared option count %d",
						i, hb, tc.options)
				}
				if m.HighestBit() >= 64 {
					highBitSeen = true
				}
				for bit, ok := m.NextBit(0); ok; bit, ok = m.NextBit(bit + 1) {
					counts[bit]++
				}
			}
			if !highBitSeen {
				t.Fatalf("%s: no generated row ever set a bit at or above 64 — "+
					"the high words are being dropped", tc.typeName)
			}

			// Every probed index is checked, not just the high ones: an
			// implementation that shifted the whole mask would move the
			// low frequencies too.
			for idx := range tc.probe {
				got := float64(counts[idx]) / float64(rowCount)
				if math.Abs(got-freqs[idx]) > tolerance {
					t.Errorf("option %q (bit %d) frequency = %.4f, want within %.2f of declared %.2f",
						names[idx], idx, got, tolerance, freqs[idx])
				}
			}
			// The unprobed remainder must also sit at its declared base,
			// so a bug confined to "every bit except the ones the test
			// names" cannot pass.
			for idx := range names {
				if _, probed := tc.probe[idx]; probed {
					continue
				}
				got := float64(counts[idx]) / float64(rowCount)
				if math.Abs(got-0.5) > tolerance {
					t.Errorf("option %q (bit %d) frequency = %.4f, want within %.2f of base 0.50",
						names[idx], idx, got, tolerance)
				}
			}
		})
	}
}

// TestSynth_SetBernoulli_WideRungDeterministic extends the determinism
// contract (skills/synthetic-data.md) to the wide rungs: identical
// (spec, seed) must produce byte-identical output, including the 16-
// and 32-byte payloads whose word order is the normative wire contract.
func TestSynth_SetBernoulli_WideRungDeterministic(t *testing.T) {
	for _, tc := range []struct {
		typeName string
		options  int
	}{
		{"set_u128", 128},
		{"set_u256", 256},
	} {
		t.Run(tc.typeName, func(t *testing.T) {
			params, _, _ := wideSetParams(tc.options, 0.5, map[int]float64{70: 0.3, tc.options - 1: 0.7})
			spec := &synth.Spec{
				RowCount: 300,
				Fields: []synth.FieldSpec{
					{Name: "features", Type: tc.typeName,
						Distribution: synth.DistSetBernoulli, Params: params},
				},
			}
			a, _, err := synth.SynthBytes(spec, synth.Options{Seed: 42})
			if err != nil {
				t.Fatalf("first synth: %v", err)
			}
			b, _, err := synth.SynthBytes(spec, synth.Options{Seed: 42})
			if err != nil {
				t.Fatalf("second synth: %v", err)
			}
			if !bytes.Equal(a, b) {
				t.Fatalf("byte mismatch for identical (spec, seed): len(a)=%d len(b)=%d", len(a), len(b))
			}
			// A different seed must actually move the bytes, otherwise
			// "deterministic" would be satisfiable by writing constants.
			c, _, err := synth.SynthBytes(spec, synth.Options{Seed: 43})
			if err != nil {
				t.Fatalf("third synth: %v", err)
			}
			if bytes.Equal(a, c) {
				t.Fatal("a different seed produced identical bytes; the draw is not seeded")
			}
		})
	}
}

// TestSynth_SetU256_SampleReadsBackExpectedLabels is the round-trip
// acceptance bar: a generated set_u256 cohort must read back through
// the ordinary sample path with the expected labels. Frequencies are
// pinned to 1.0 / 0.0 so the expected selection is exact rather than
// statistical, and the selected bits straddle every 64-bit word of the
// 256-bit mask — word order is the normative wire contract and a
// reversed pair of words would decode as a different, plausible
// selection.
func TestSynth_SetU256_SampleReadsBackExpectedLabels(t *testing.T) {
	const options = 256
	selected := []int{0, 64, 130, 200, 255}
	probe := make(map[int]float64, len(selected))
	for _, i := range selected {
		probe[i] = 1.0
	}
	params, names, _ := wideSetParams(options, 0.0, probe)

	want := make([]string, 0, len(selected))
	for _, i := range selected {
		want = append(want, names[i])
	}

	spec := &synth.Spec{
		RowCount: 25,
		Fields: []synth.FieldSpec{
			{Name: "features", Type: "set_u256",
				Distribution: synth.DistSetBernoulli, Params: params},
		},
	}

	memfs := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: memfs})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	if _, err := p.Synth(context.Background(), spec, "/wide.pulse", pulse.SynthOptions{Seed: 5}); err != nil {
		t.Fatalf("Synth: %v", err)
	}

	rows, err := p.Sample(context.Background(), "/wide.pulse", 25)
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	if len(rows) != 25 {
		t.Fatalf("sampled %d rows, want 25", len(rows))
	}
	for i, row := range rows {
		got, ok := row["features"].([]string)
		if !ok {
			t.Fatalf("row %d: features is %T (%v), want []string", i, row["features"], row["features"])
		}
		if len(got) != len(want) {
			t.Fatalf("row %d: features = %v, want %v", i, got, want)
		}
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("row %d: features = %v, want %v", i, got, want)
			}
		}
	}
}

// TestSynth_SetBernoulli_WideEmptyMaskIsGeneratedAndDistinctFromNull
// pins the set_* semantic that survives the width change: an all-zero
// mask is a valid "no selection" and is NOT a null. A nullable wide set
// with low per-option frequencies must produce all three states —
// null, non-null-empty and non-null-populated — in one run.
func TestSynth_SetBernoulli_WideEmptyMaskIsGeneratedAndDistinctFromNull(t *testing.T) {
	params, _, _ := wideSetParams(128, 0.01, nil)
	spec := &synth.Spec{
		RowCount: 800,
		Fields: []synth.FieldSpec{
			{Name: "features", Type: "set_u128", Distribution: synth.DistSetBernoulli,
				Nullable: true, NullRate: 0.3, Params: params},
		},
	}
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 19})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	masks, nullFlags, _ := readWideSetFieldRows(t, data, "features")
	if len(masks) != 800 {
		t.Fatalf("row count = %d, want 800", len(masks))
	}
	var sawNull, sawEmpty, sawPopulated bool
	for i, m := range masks {
		switch {
		case nullFlags[i]:
			sawNull = true
		case m.IsEmpty():
			sawEmpty = true
		default:
			sawPopulated = true
		}
	}
	if !sawNull || !sawEmpty || !sawPopulated {
		t.Fatalf("want all three states; sawNull=%v sawEmpty=%v sawPopulated=%v",
			sawNull, sawEmpty, sawPopulated)
	}
}

// TestSynth_SetSpec_OptionsBeyondRungCapacityRefused pins that a spec
// declaring more members than the rung addresses is refused with a
// coded error rather than quietly losing the overflow options. The
// table spans narrow and wide rungs so the refusal is derived from
// MaxSetEntries rather than hardcoded at 64.
func TestSynth_SetSpec_OptionsBeyondRungCapacityRefused(t *testing.T) {
	for _, tc := range []struct {
		typeName string
		capacity int
	}{
		{"set_u8", 8},
		{"set_u64", 64},
		{"set_u128", 128},
		{"set_u256", 256},
	} {
		t.Run(tc.typeName, func(t *testing.T) {
			params, _, _ := wideSetParams(tc.capacity+1, 0.5, nil)
			spec := &synth.Spec{
				RowCount: 4,
				Fields: []synth.FieldSpec{
					{Name: "features", Type: tc.typeName,
						Distribution: synth.DistSetBernoulli, Params: params},
				},
			}
			_, _, err := synth.SynthBytes(spec, synth.Options{Seed: 1})
			if err == nil {
				t.Fatalf("%s accepted %d options, want a refusal", tc.typeName, tc.capacity+1)
			}
			if !errors.HasCode(err, errors.PULSE_IMPORT_SET_OVERFLOW) {
				t.Fatalf("error %v does not carry PULSE_IMPORT_SET_OVERFLOW", err)
			}

			// Exactly at capacity must still be accepted, so the
			// refusal cannot be satisfied by rejecting every wide spec.
			okParams, _, _ := wideSetParams(tc.capacity, 0.5, nil)
			okSpec := &synth.Spec{
				RowCount: 4,
				Fields: []synth.FieldSpec{
					{Name: "features", Type: tc.typeName,
						Distribution: synth.DistSetBernoulli, Params: okParams},
				},
			}
			if _, _, err := synth.SynthBytes(okSpec, synth.Options{Seed: 1}); err != nil {
				t.Fatalf("%s refused a spec at exactly its %d-option capacity: %v",
					tc.typeName, tc.capacity, err)
			}
		})
	}
}

// TestSynth_SetWideRungStride pins that the record stride the writer
// lays down matches the schema's own derived stride. Stride is a pure
// function of the type byte, so a wide set field written at the wrong
// width shifts every following column by a fixed offset and still
// decodes into plausible numbers.
func TestSynth_SetWideRungStride(t *testing.T) {
	for _, tc := range []struct {
		typeName   string
		options    int
		wantBytes  int
		rowCount   int
		neighbourV float64
	}{
		{"set_u128", 128, 16, 40, 7},
		{"set_u256", 256, 32, 40, 7},
	} {
		t.Run(tc.typeName, func(t *testing.T) {
			params, _, _ := wideSetParams(tc.options, 0.5, nil)
			spec := &synth.Spec{
				RowCount: tc.rowCount,
				Fields: []synth.FieldSpec{
					{Name: "features", Type: tc.typeName,
						Distribution: synth.DistSetBernoulli, Params: params},
					// A neighbour AFTER the wide field: a short write
					// would be read back as part of this column.
					{Name: "tail", Type: "u8", Distribution: synth.DistConstant,
						Params: map[string]any{"value": tc.neighbourV}},
				},
			}
			data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 3})
			if err != nil {
				t.Fatalf("SynthBytes: %v", err)
			}
			r := bytes.NewReader(data)
			if err := encoding.ReadHeader(r); err != nil {
				t.Fatalf("read header: %v", err)
			}
			schema, err := encoding.ReadSchema(r)
			if err != nil {
				t.Fatalf("read schema: %v", err)
			}
			f := schema.Field("features")
			if f == nil || f.Type.ByteSize() != tc.wantBytes {
				t.Fatalf("features ByteSize = %d, want %d", f.Type.ByteSize(), tc.wantBytes)
			}
			rr := encoding.NewRecordReader(r, schema)
			values := make(map[string]float64)
			nulls := make(map[string]bool)
			wide := make(map[string]any)
			rows := 0
			for {
				if err := rr.ReadRecordWithWide(values, nulls, wide); err != nil {
					break
				}
				rows++
				if values["tail"] != tc.neighbourV {
					t.Fatalf("row %d: tail = %v, want %v — the wide set field wrote the wrong stride",
						rows, values["tail"], tc.neighbourV)
				}
			}
			if rows != tc.rowCount {
				t.Fatalf("decoded %d rows, want %d", rows, tc.rowCount)
			}
		})
	}
}

// TestSynth_SetU256_ConstantSelectionCoercesAtWideRung exercises the
// coercion matrix's "set selection -> set" cell (synth/rules_coerce.go)
// at a wide rung: a map[string]bool carrying a member above bit 64 must
// reach the file intact. The matrix is class-keyed (ft.IsSet()), so the
// only thing that could go wrong is the encode width — which is exactly
// what a silent truncation looks like.
func TestSynth_SetU256_ConstantSelectionCoercesAtWideRung(t *testing.T) {
	const options = 256
	names := wideSetOptionNames(options)
	optionsAny := make([]any, options)
	freqsAny := make([]any, options)
	for i := range names {
		optionsAny[i] = names[i]
		freqsAny[i] = 0.0
	}
	sel := map[string]bool{names[5]: true, names[64]: true, names[249]: true}

	spec := &synth.Spec{
		RowCount: 12,
		Fields: []synth.FieldSpec{
			{Name: "features", Type: "set_u256", Distribution: synth.DistConstant,
				Params: map[string]any{
					"options": optionsAny, "frequencies": freqsAny,
					"value": sel,
				}},
		},
	}
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 2})
	if err != nil {
		t.Fatalf("SynthBytes: %v", err)
	}
	masks, nullFlags, dict := readWideSetFieldRows(t, data, "features")
	if len(masks) != 12 {
		t.Fatalf("row count = %d, want 12", len(masks))
	}
	want := []string{names[5], names[64], names[249]}
	for i, m := range masks {
		if nullFlags[i] {
			t.Fatalf("row %d unexpectedly null", i)
		}
		got := m.Labels(dict)
		if len(got) != len(want) {
			t.Fatalf("row %d labels = %v, want %v", i, got, want)
		}
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("row %d labels = %v, want %v", i, got, want)
			}
		}
	}
}
