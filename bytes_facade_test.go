package pulse_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Facade coverage for InspectBytes / PredictBytes — the instance
// methods that replaced descriptor's free byte-level functions. The
// free functions took their options from the caller, and every caller
// that passed nil (or forgot the snapshot) silently got the
// built-in-only path: an embedder-registered operator was flagged
// unknown. These tests pin that the instance fills what it owns.

// bytesFacadeCohort returns the raw bytes of a two-field cohort —
// score f64 and seg categorical_u8 — with `records` whole records and
// `tail` extra trailing bytes (a half-written record when non-zero).
func bytesFacadeCohort(t *testing.T, records, tail int) []byte {
	t.Helper()
	dict := encoding.NewDictionary()
	for _, v := range []string{"a", "b"} {
		if _, err := dict.Add(v); err != nil {
			t.Fatalf("dict.Add: %v", err)
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 0},
		{Name: "seg", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 8, Dictionary: dict},
	}}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	buf.Write(make([]byte, records*9+tail))
	return buf.Bytes()
}

func envHasCode(entries []*descriptor.EnvelopeEntry, code string) bool {
	for _, e := range entries {
		if e.Code == code {
			return true
		}
	}
	return false
}

func newBytesFacadePulse(t *testing.T, opts pulse.Options) *pulse.Pulse {
	t.Helper()
	opts.FS = afero.NewMemMapFs()
	p, err := pulse.New(opts)
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	return p
}

// A registered extension operator is KNOWN to PredictBytes — the
// snapshot comes from the instance, not from the caller.
func TestPredictBytes_ExtensionOperatorIsKnown(t *testing.T) {
	data := bytesFacadeCohort(t, 2, 0)
	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score", Label: "s"}},
		Features:     []*types.Feature{{Type: "FEAT_ACME_DOUBLE", Field: "score"}},
	}

	p := newBytesFacadePulse(t, pulse.Options{Extensions: pulse.Extensions{
		Features: []pulse.FeatureRegistration{{
			Name: "FEAT_ACME_DOUBLE", Factory: stubFeatureFactory, Streamable: true,
		}},
	}})
	env, err := p.PredictBytes(context.Background(), data, req)
	if err != nil {
		t.Fatalf("PredictBytes: %v", err)
	}
	for _, e := range env.Errors {
		if strings.Contains(e.Message, "FEAT_ACME_DOUBLE") {
			t.Errorf("registered extension feature flagged: %s %s", e.Code, e.Message)
		}
	}
	if res := env.Data.(*descriptor.PredictResult); !res.Valid {
		t.Errorf("Valid = false with errors %+v; want true", env.Errors)
	}

	// The control: the same request on an instance without the
	// registration IS flagged, so the assertion above is not vacuous.
	bare := newBytesFacadePulse(t, pulse.Options{})
	benv, err := bare.PredictBytes(context.Background(), data, req)
	if err != nil {
		t.Fatalf("PredictBytes(bare): %v", err)
	}
	if res := benv.Data.(*descriptor.PredictResult); res.Valid {
		t.Error("bare instance: Valid = true; the unregistered feature should be flagged unknown")
	}
}

// Options.Strict promotes a predict warning into an error.
func TestPredictBytes_HonoursStrict(t *testing.T) {
	data := bytesFacadeCohort(t, 2, 0)
	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "seg", Label: "s"}},
	}
	const code = "PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL"

	lax, err := newBytesFacadePulse(t, pulse.Options{}).PredictBytes(context.Background(), data, req)
	if err != nil {
		t.Fatalf("PredictBytes(lax): %v", err)
	}
	if !envHasCode(lax.Warnings, code) || envHasCode(lax.Errors, code) {
		t.Fatalf("lax: warnings %+v errors %+v; want %s as a warning only", lax.Warnings, lax.Errors, code)
	}

	strict, err := newBytesFacadePulse(t, pulse.Options{Strict: true}).PredictBytes(context.Background(), data, req)
	if err != nil {
		t.Fatalf("PredictBytes(strict): %v", err)
	}
	if !envHasCode(strict.Errors, code) {
		t.Errorf("strict: errors %+v; want %s promoted to an error", strict.Errors, code)
	}
}

// Options.EchoRequest populates envelope.Request.
func TestPredictBytes_HonoursEchoRequest(t *testing.T) {
	data := bytesFacadeCohort(t, 2, 0)
	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "score", Label: "s"}},
	}
	off, err := newBytesFacadePulse(t, pulse.Options{}).PredictBytes(context.Background(), data, req)
	if err != nil {
		t.Fatalf("PredictBytes: %v", err)
	}
	if off.Request != nil {
		t.Errorf("echo off: envelope.Request = %+v, want nil", off.Request)
	}
	on, err := newBytesFacadePulse(t, pulse.Options{EchoRequest: true}).PredictBytes(context.Background(), data, req)
	if err != nil {
		t.Fatalf("PredictBytes: %v", err)
	}
	if on.Request == nil {
		t.Error("echo on: envelope.Request = nil, want the normalized request")
	}
}

func TestPredictBytes_NilRequestAndCancelledContextError(t *testing.T) {
	p := newBytesFacadePulse(t, pulse.Options{})
	data := bytesFacadeCohort(t, 1, 0)
	if _, err := p.PredictBytes(context.Background(), data, nil); err == nil {
		t.Error("nil request: want an error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.PredictBytes(ctx, data, &types.Request{}); err == nil {
		t.Error("cancelled ctx: want an error")
	}
	if _, err := p.InspectBytes(ctx, data, nil); err == nil {
		t.Error("InspectBytes cancelled ctx: want an error")
	}
}

// InspectBytes returns the envelope whole: a half-written trailing
// record floors the count AND raises the truncated-tail warning.
func TestInspectBytes_SurfacesTruncatedTailWarning(t *testing.T) {
	p := newBytesFacadePulse(t, pulse.Options{})

	clean, err := p.InspectBytes(context.Background(), bytesFacadeCohort(t, 3, 0), nil)
	if err != nil {
		t.Fatalf("InspectBytes: %v", err)
	}
	if len(clean.Warnings) != 0 {
		t.Errorf("clean cohort warned: %+v", clean.Warnings)
	}

	env, err := p.InspectBytes(context.Background(), bytesFacadeCohort(t, 3, 4), nil)
	if err != nil {
		t.Fatalf("InspectBytes: %v", err)
	}
	if got := env.Data.(*descriptor.InspectResult).RecordCount; got != 3 {
		t.Errorf("record_count = %d, want 3 (floored)", got)
	}
	if !envHasCode(env.Warnings, "ENCODING_INVALID") {
		t.Errorf("warnings = %+v, want the ENCODING_INVALID truncated-tail warning", env.Warnings)
	}

	// Options pass through: a dictionary limit of 1 truncates seg.
	capped, err := p.InspectBytes(context.Background(), bytesFacadeCohort(t, 3, 0),
		&descriptor.InspectOptions{DictionaryLimit: 1})
	if err != nil {
		t.Fatalf("InspectBytes: %v", err)
	}
	for _, f := range capped.Data.(*descriptor.InspectResult).Fields {
		if f.Name == "seg" && (f.Dictionary == nil || !f.Dictionary.Truncated) {
			t.Errorf("seg dictionary %+v, want truncated under DictionaryLimit:1", f.Dictionary)
		}
	}
}

// A malformed byte slice is an envelope error, not a Go error, so a
// --json caller can emit it verbatim.
func TestInspectBytes_BadHeaderIsAnEnvelopeError(t *testing.T) {
	env, err := newBytesFacadePulse(t, pulse.Options{}).InspectBytes(context.Background(), []byte("nope"), nil)
	if err != nil {
		t.Fatalf("InspectBytes: %v", err)
	}
	if !envHasCode(env.Errors, "ENCODING_INVALID") {
		t.Errorf("errors = %+v, want ENCODING_INVALID", env.Errors)
	}
}
