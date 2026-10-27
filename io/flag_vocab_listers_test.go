package io_test

import (
	stderrors "errors"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/errors"
	iio "github.com/frankbardon/pulse/internal/io"
	"github.com/frankbardon/pulse/internal/io/spss"
	pio "github.com/frankbardon/pulse/io"
)

func TestFloatWidths_ResolvedByTheJob(t *testing.T) {
	t.Parallel()

	got := pio.FloatWidths()
	if !slices.Equal(got, []string{"f64", "f32"}) {
		t.Fatalf("FloatWidths() = %v", got)
	}
	got[0] = "mutated"
	if pio.FloatWidths()[0] != "f64" {
		t.Fatal("FloatWidths shares its backing array")
	}
	for _, w := range pio.FloatWidths() {
		ft, err := iio.ResolveFloatWidth(w)
		if err != nil || ft.String() != w {
			t.Errorf("ResolveFloatWidth(%q) = (%v, %v)", w, ft, err)
		}
	}
	_, err := iio.ResolveFloatWidth("f16")
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_IMPORT_FLOAT_WIDTH_INVALID ||
		ce.Message != `float width "f16" is not one of f64, f32` || !slices.Equal(ce.Details["valid"].([]string), pio.FloatWidths()) {
		t.Fatalf("f16: %v (details %v)", err, ce)
	}
}

func TestDSTPolicies_ParsedByTheLibrary(t *testing.T) {
	t.Parallel()

	want := []pio.DSTPolicy{pio.DSTPolicyError, pio.DSTPolicyEarlier, pio.DSTPolicyLater}
	got := pio.DSTPolicies()
	if !slices.Equal(got, want) {
		t.Fatalf("DSTPolicies() = %v", got)
	}
	got[0] = "mutated"
	if pio.DSTPolicies()[0] != pio.DSTPolicyError {
		t.Fatal("DSTPolicies shares its backing array")
	}
	for _, p := range want {
		if g, err := pio.ParseDSTPolicy(string(p)); err != nil || g != p {
			t.Errorf("ParseDSTPolicy(%q) = (%q, %v)", p, g, err)
		}
	}
	if g, err := pio.ParseDSTPolicy(""); err != nil || g != pio.DSTPolicyError {
		t.Errorf("empty = (%q, %v), want the default", g, err)
	}
	for _, bad := range []string{"Error", "late", " later", "none"} {
		_, err := pio.ParseDSTPolicy(bad)
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.SERVICE_VALIDATION ||
			ce.Details[iio.DetailOption] != "dst_policy" || ce.Details["value"] != bad {
			t.Errorf("ParseDSTPolicy(%q): %v", bad, err)
		}
	}
}

func TestSPSSMissingModes_ParsedByTheReader(t *testing.T) {
	t.Parallel()

	got := pio.SPSSMissingModes()
	if !slices.Equal(got, []pio.SPSSMissingMode{pio.SPSSMissingAuto, pio.SPSSMissingNull}) {
		t.Fatalf("SPSSMissingModes() = %v", got)
	}
	got[0] = "mutated"
	if pio.SPSSMissingModes()[0] != pio.SPSSMissingAuto {
		t.Fatal("SPSSMissingModes shares its backing array")
	}
	for _, m := range pio.SPSSMissingModes() {
		pm, err := spss.ParseMissingMode(string(m))
		if err != nil || pm.String() != string(m) {
			t.Errorf("spss.ParseMissingMode(%q) = (%v, %v)", m, pm, err)
		}
	}
}
