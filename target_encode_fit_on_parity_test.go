package pulse_test

import (
	"context"
	stderrors "errors"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
)

// The model-workbench E3-S3 parity suite for FEAT_TARGET_ENCODE
// params.fit_on / folds: the leakage set, its strict refusal and the
// fit_on compile are the same on predict and Process.

const (
	teSplit = `{"type":"FEAT_TRAIN_TEST_SPLIT","label":"split","params":{"ratios":[0.5,0.5],"seed":1}}`
	teCount = `],"aggregations":[{"type":"AGG_COUNT","field":"x"}]}`
)

// TestTargetEncodeFitOn_LeakageParity: fit_on silences
// PULSE_FEAT_TARGET_LEAKAGE_RISK on both sides and strict runs it; folds
// without fit_on keeps the reworded warning — the same message and
// details on both sides — and strict refuses it on both.
func TestTargetEncodeFitOn_LeakageParity(t *testing.T) {
	t.Parallel()

	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	strict, err := pulse.New(pulse.Options{FS: fs, Strict: true})
	if err != nil {
		t.Fatal(err)
	}

	fitOn := `{"features":[` + teSplit + `,{"type":"FEAT_TARGET_ENCODE","field":"g","params":{"target":"x","fit_on":"split == 0","folds":3,"rng_seed":5}}` + teCount
	resp, err := p.Process(ctx, categoricalRequest(t, fitOn, cohort))
	if err != nil {
		t.Fatalf("fit_on Process: %v", err)
	}
	if got := leakageWarnings(resp.Warnings); len(got) != 0 {
		t.Errorf("fit_on runtime still warns: %v", got)
	}
	if env := predictEnvelope(t, p, fs, cohort, categoricalRequest(t, fitOn, cohort)); len(leakageEntries(env.Warnings)) != 0 || len(env.Errors) != 0 {
		t.Errorf("fit_on predict: warnings %v errors %v", entryCodes(env.Warnings), entryCodes(env.Errors))
	}
	if _, err := strict.Process(ctx, categoricalRequest(t, fitOn, cohort)); err != nil {
		t.Errorf("strict refused a fit_on encoder: %v", err)
	}
	if senv := predictEnvelope(t, strict, fs, cohort, categoricalRequest(t, fitOn, cohort)); len(leakageEntries(senv.Errors)) != 0 {
		t.Errorf("strict predict refused a fit_on encoder: %v", leakageEntries(senv.Errors))
	}

	folds := `{"features":[{"type":"FEAT_TARGET_ENCODE","field":"g","params":{"target":"x","folds":4}}` + teCount
	resp, err = p.Process(ctx, categoricalRequest(t, folds, cohort))
	if err != nil {
		t.Fatalf("folds Process: %v", err)
	}
	got := leakageWarnings(resp.Warnings)
	want := leakageEntries(predictEnvelope(t, p, fs, cohort, categoricalRequest(t, folds, cohort)).Warnings)
	if len(got) != 1 || len(want) != 1 {
		t.Fatalf("folds-only: runtime %d, predict %d leakage warnings; want one each", len(got), len(want))
	}
	if got[0].Message != want[0].Message || !reflect.DeepEqual(got[0].Details, want[0].Details) {
		t.Errorf("runtime %q %v, predict %q %v", got[0].Message, got[0].Details, want[0].Message, want[0].Details)
	}
	if got[0].Details["folds"] != 4 {
		t.Errorf("folds-only details = %v, want folds 4", got[0].Details)
	}
	_, rerr := strict.Process(ctx, categoricalRequest(t, folds, cohort))
	var ce *perr.CodedError
	if !stderrors.As(rerr, &ce) || ce.Code != perr.PULSE_FEAT_TARGET_LEAKAGE_RISK || ce.Message != want[0].Message {
		t.Fatalf("strict folds-only runtime: %v, want %s", rerr, perr.PULSE_FEAT_TARGET_LEAKAGE_RISK)
	}
	if senv := predictEnvelope(t, strict, fs, cohort, categoricalRequest(t, folds, cohort)); len(leakageEntries(senv.Errors)) != 1 {
		t.Errorf("strict folds-only predict: %v", entryCodes(senv.Errors))
	}
}

// TestTargetEncodeFitOn_CompileParity: fit_on reads only the schema and
// EARLIER features' columns — a forward reference to a later split, a
// self reference, an unknown name and a non-bool result are the same
// PULSE_FORMULA_COMPILE_ERROR (code, message, details) on both sides.
func TestTargetEncodeFitOn_CompileParity(t *testing.T) {
	t.Parallel()

	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	cases := map[string]string{
		"forward reference": `{"features":[{"type":"FEAT_TARGET_ENCODE","field":"g","params":{"target":"x","fit_on":"split == 0"}},` + teSplit + teCount,
		"self reference":    `{"features":[{"type":"FEAT_TARGET_ENCODE","field":"g","label":"te","params":{"target":"x","fit_on":"te > 1"}}` + teCount,
		"unknown name":      `{"features":[{"type":"FEAT_TARGET_ENCODE","field":"g","params":{"target":"x","fit_on":"nope == 1"}}` + teCount,
		// A non-bool fit_on is refused at compile (exprenv.ResultRefusal),
		// never PROCESSING_RUNTIME on the first row (model-workbench E3-S4).
		"non-bool":                `{"features":[{"type":"FEAT_TARGET_ENCODE","field":"g","params":{"target":"x","fit_on":"x + 1"}}` + teCount,
		"non-bool earlier column": `{"features":[` + teSplit + `,{"type":"FEAT_TARGET_ENCODE","field":"g","params":{"target":"x","fit_on":"split + 1"}}` + teCount,
	}
	for name, body := range cases {
		_, rerr := p.Process(ctx, categoricalRequest(t, body, cohort))
		var ce *perr.CodedError
		if !stderrors.As(rerr, &ce) {
			t.Errorf("%s: runtime %v, want a coded refusal", name, rerr)
			continue
		}
		if ce.Code != perr.PULSE_FORMULA_COMPILE_ERROR {
			t.Errorf("%s: runtime %s, want %s", name, ce.Code, perr.PULSE_FORMULA_COMPILE_ERROR)
		}
		env := predictEnvelope(t, p, fs, cohort, categoricalRequest(t, body, cohort))
		t.Run(name, func(t *testing.T) { sameEntry(t, env, rerr) })
	}
}

// TestTargetEncodeFitOn_ParamRefusalParity: the new params' refusals
// are PROCESSING_CONFIG with the same message on both sides, predict
// reporting each once.
func TestTargetEncodeFitOn_ParamRefusalParity(t *testing.T) {
	t.Parallel()

	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	for name, params := range map[string]string{
		"one fold":           `{"target":"x","folds":1}`,
		"too many folds":     `{"target":"x","folds":101}`,
		"seed without folds": `{"target":"x","rng_seed":3}`,
		"blank fit_on":       `{"target":"x","fit_on":" "}`,
		"fit_on not string":  `{"target":"x","fit_on":true}`,
	} {
		body := `{"features":[{"type":"FEAT_TARGET_ENCODE","field":"g","params":` + params + `}` + teCount
		_, rerr := p.Process(ctx, categoricalRequest(t, body, cohort))
		var ce *perr.CodedError
		if !stderrors.As(rerr, &ce) || ce.Code != perr.PROCESSING_CONFIG {
			t.Errorf("%s: runtime %v, want %s", name, rerr, perr.PROCESSING_CONFIG)
			continue
		}
		env := predictEnvelope(t, p, fs, cohort, categoricalRequest(t, body, cohort))
		if len(env.Errors) != 1 || env.Errors[0].Code != string(ce.Code) {
			t.Errorf("%s: predict %v, want one %s (runtime %q)", name, entryCodes(env.Errors), ce.Code, ce.Message)
			continue
		}
		if name != "fit_on not string" && env.Errors[0].Message != ce.Message {
			t.Errorf("%s: predict %q, runtime %q", name, env.Errors[0].Message, ce.Message)
		}
	}
}

// TestTargetEncodeFitOn_ZonedRequestRuns: a request zone reaches a
// fit_on calling to_day (the inherited zone rides the feature slot) and
// neither side refuses the slot for it.
func TestTargetEncodeFitOn_ZonedRequestRuns(t *testing.T) {
	t.Parallel()

	p, fs, cohort := acceptanceCohort(t)
	body := `{"time_zone":"America/New_York","features":[{"type":"FEAT_TARGET_ENCODE","field":"g","params":{"target":"x","fit_on":"to_day(t_datetime) > 0"}}` + teCount
	if _, err := p.Process(context.Background(), categoricalRequest(t, body, cohort)); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if env := predictEnvelope(t, p, fs, cohort, categoricalRequest(t, body, cohort)); len(env.Errors) != 0 {
		t.Errorf("predict: %v", entryCodes(env.Errors))
	}
}
