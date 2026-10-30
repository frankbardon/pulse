package integration_test

import (
	"context"
	"math"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// sem_group_empty_test.go is a grouped MAT_SEM fit over a group field
// whose declared levels outnumber the levels holding rows after the
// filters (issue #393): the empty level is named by
// PULSE_MATRIX_GROUP_LEVEL_EMPTY, a := reading the empty level's
// per-group label is a null row with PULSE_MATRIX_DEFINED_NONFINITE
// (reason empty_group), both reach Response.Warnings (the envelope's
// top-level warnings) and both fail the run with their own code under
// Strict.

// threeWaveModel labels the service path per wave; d31 reads wave 3's
// label, d21 only waves 1 and 2.
const threeWaveModel = "quality =~ q1 + q2 + q3\nvalue =~ v1 + v2 + v3\nservice =~ s1 + s2 + s3\n" +
	"recommend ~ quality + value + c(b1, b2, b3)*service\nd31 := b3 - b1\nd21 := b2 - b1"

// threeWaveCohort splits the brand survey's second wave in two: rows
// 900 on are wave "3".
func threeWaveCohort(t *testing.T, opts pulse.Options) *pulse.Pulse {
	t.Helper()
	p, _ := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8, dict: []string{"1", "2", "3"},
		relabel: func(r int, w string) string {
			if r >= 900 {
				return "3"
			}
			return w
		}}.build(t, opts)
	return p
}

// threeWaveRequest filters the three-wave cohort to waves 1 and 2.
func threeWaveRequest(t *testing.T) *types.Request {
	req := semGroupRequest(t, "brand_def", map[string]any{"model": threeWaveModel})
	req.Filterers = []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "wave", Values: []string{"1", "2"}}}
	return req
}

func warningOf(ws []*types.ResponseWarning, code errors.Code) *types.ResponseWarning {
	for _, w := range ws {
		if w != nil && w.Code == string(code) {
			return w
		}
	}
	return nil
}

// TestSEM_GroupLevelEmptyIsNeverSilent is issue #393's scenario.
func TestSEM_GroupLevelEmptyIsNeverSilent(t *testing.T) {
	t.Parallel()

	p := threeWaveCohort(t, pulse.Options{})
	resp, err := p.Process(context.Background(), threeWaveRequest(t))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.Warnings) == 0 {
		t.Fatal("Response.Warnings is empty: the empty wave and the null := row are silent")
	}
	res := resp.Matrices[0]
	if res.Error != nil {
		t.Fatalf("slot failed: %+v", res.Error)
	}
	_, row := semRow(t, res, "d31 := b3-b1")
	for j, v := range row {
		if !math.IsNaN(v) {
			t.Errorf("d31 column %d = %g, want null", j, v)
		}
	}
	if _, row := semRow(t, res, "d21 := b2-b1"); math.IsNaN(row[0]) {
		t.Errorf("d21 reads waves with rows: %v", row)
	}
	for _, ws := range [][]*types.ResponseWarning{resp.Warnings, res.Warnings} {
		empty := warningOf(ws, errors.PULSE_MATRIX_GROUP_LEVEL_EMPTY)
		if empty == nil {
			t.Fatalf("warnings %+v, want PULSE_MATRIX_GROUP_LEVEL_EMPTY", ws)
		}
		if lv, _ := empty.Details["levels"].([]string); len(lv) != 1 || lv[0] != "3" {
			t.Errorf("details.levels = %v, want [3]", empty.Details["levels"])
		}
		def := warningOf(ws, errors.PULSE_MATRIX_DEFINED_NONFINITE)
		if def == nil {
			t.Fatalf("warnings %+v, want PULSE_MATRIX_DEFINED_NONFINITE", ws)
		}
		if def.Details["reason"] != "empty_group" || def.Details["label"] != "b3" || def.Details["level"] != "3" {
			t.Errorf("DEFINED_NONFINITE details %v, want reason empty_group, label b3, level 3", def.Details)
		}
		for _, w := range []*types.ResponseWarning{empty, def} {
			if _, bare := w.Details["group"]; bare {
				t.Errorf("%s details carry a bare group key: %v", w.Code, w.Details)
			}
			if w.Details["group_field"] != "wave" || w.Details["matrix"] != "sem" {
				t.Errorf("%s details %v, want group_field wave, matrix sem", w.Code, w.Details)
			}
		}
	}
	// The lifted copy locates its slot.
	if w := warningOf(resp.Warnings, errors.PULSE_MATRIX_GROUP_LEVEL_EMPTY); w.Details["index"] != 0 {
		t.Errorf("lifted details.index = %v, want 0", w.Details["index"])
	}
}

// TestSEM_GroupLevelEmptyStrict: under Strict each warning fails the
// run with its own code, located at its slot.
func TestSEM_GroupLevelEmptyStrict(t *testing.T) {
	t.Parallel()

	threeWave := threeWaveCohort(t, pulse.Options{Strict: true})
	brand, _ := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{Strict: true})
	for _, c := range []struct {
		name string
		p    *pulse.Pulse
		req  func() *types.Request
		code errors.Code
	}{
		// The := reads no empty level: the empty level alone warns.
		{"level empty", threeWave, func() *types.Request {
			req := threeWaveRequest(t)
			req.Matrices[0].Params = setParam(req.Matrices[0].Params, "model",
				"quality =~ q1 + q2 + q3\nvalue =~ v1 + v2 + v3\nservice =~ s1 + s2 + s3\nrecommend ~ quality + value + c(b1, b2, b3)*service\nd21 := b2 - b1")
			return req
		}, errors.PULSE_MATRIX_GROUP_LEVEL_EMPTY},
		// An ungrouped := undefined at the estimate warns alone.
		{"defined nonfinite", brand, func() *types.Request {
			return semGroupRequest(t, "brand_def", map[string]any{"group": nil,
				"model": "quality =~ q1 + q2 + q3\nvalue =~ v1 + v2 + v3\nservice =~ s1 + s2 + s3\nrecommend ~ bq*quality + bv*value + bs*service\nr := bq/(bv - bv)"})
		}, errors.PULSE_MATRIX_DEFINED_NONFINITE},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := c.p.Process(context.Background(), c.req())
			var ce *errors.CodedError
			if !asCoded(err, &ce) || ce.Code != c.code || ce.Details["slot_kind"] != "matrices" || ce.Details["label"] != "sem" {
				t.Fatalf("Strict error %v (%v), want %s on matrices sem", err, semDetailsOf(ce), c.code)
			}
		})
	}
}

// TestSEM_GroupLevelEmptyOrdinal: the grouped ordinal fit warns the
// same way.
func TestSEM_GroupLevelEmptyOrdinal(t *testing.T) {
	t.Parallel()

	p := threeWaveCohort(t, pulse.Options{})
	req := threeWaveRequest(t)
	req.Matrices[0].Params = setParam(req.Matrices[0].Params, "estimator", "wlsmv")
	req.Matrices[0].Params = setParam(req.Matrices[0].Params, "ordered", brandOrdered)
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if res := resp.Matrices[0]; res.Error != nil {
		t.Fatalf("slot failed: %+v", res.Error)
	}
	empty := warningOf(resp.Warnings, errors.PULSE_MATRIX_GROUP_LEVEL_EMPTY)
	def := warningOf(resp.Warnings, errors.PULSE_MATRIX_DEFINED_NONFINITE)
	if empty == nil || def == nil || def.Details["reason"] != "empty_group" || def.Details["label"] != "b3" {
		t.Fatalf("warnings %+v, want GROUP_LEVEL_EMPTY and DEFINED_NONFINITE empty_group over b3", resp.Warnings)
	}
}
