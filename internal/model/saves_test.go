package model

import (
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// saves_test.go pins the batch save rule: every save path in a request
// or Compose batch is judged before anything runs, and two saves naming
// one file are refused, naming both writers.

func saveReq(paths ...string) *types.Request {
	r := olsReq()
	for _, p := range paths {
		spec := ols()
		spec.Save = p
		r.Regressions = append(r.Regressions, spec)
	}
	return r
}

func TestCheckSaveDuplicates(t *testing.T) {
	t.Parallel()

	a := "m/a" + FileSuffix
	if cerr := CheckRequestSaves(saveReq(a, "m/b"+FileSuffix)); cerr != nil {
		t.Fatalf("distinct paths refused: %v", cerr)
	}
	cerr := CheckRequestSaves(saveReq(a, "m/b"+FileSuffix, "./m/../m/a"+FileSuffix))
	want := map[string]any{"path": "./m/../m/a" + FileSuffix, "reason": ReasonSaveDuplicate, "sites": []any{"regressions[0]", "regressions[2]"}}
	if cerr == nil || cerr.Code != errors.PULSE_MODEL_SAVE_INVALID || !reflect.DeepEqual(cerr.Details, want) {
		t.Fatalf("got %v %+v, want %+v", cerr, cerr, want)
	}
	// A bad path is judged before uniqueness.
	if cerr := CheckRequestSaves(saveReq(a, a, "x.json")); cerr == nil || cerr.Details["reason"] != ReasonSaveSuffix {
		t.Fatalf("bad path: %v", cerr)
	}
}

// batchCase is one CheckComposeBatch case.
type batchCase struct {
	name     string
	fits     []types.FitSpec
	requests []*types.Request
	code     errors.Code
	details  map[string]any
}

func runBatchCases(t *testing.T, cases []batchCase) {
	t.Helper()
	runBatchCasesWithin(t, nil, cases)
}

// runBatchCasesWithin runs cases against outer, the registered
// populations (nil: none).
func runBatchCasesWithin(t *testing.T, outer Catalog, cases []batchCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cerr := CheckComposeBatch(tc.fits, tc.requests, outer)
			if tc.code == "" {
				if cerr != nil {
					t.Fatalf("refused: %v", cerr)
				}
				return
			}
			if cerr == nil || cerr.Code != tc.code || !reflect.DeepEqual(cerr.Details, tc.details) {
				t.Fatalf("got %v %+v, want %s %+v", cerr, cerr, tc.code, tc.details)
			}
		})
	}
}

// TestCheckComposeBatch_Saves: every slot's save path (details.request)
// and every save's uniqueness across fits, fit requests and slots.
func TestCheckComposeBatch_Saves(t *testing.T) {
	t.Parallel()

	a, b := "a"+FileSuffix, "b"+FileSuffix
	fits := []types.FitSpec{{Name: "f", Request: saveReq(b), Save: a}}
	slot := func(saves ...string) *types.Request { return saveReq(saves...) }
	runBatchCases(t, []batchCase{
		{"clean", fits, []*types.Request{slot("c" + FileSuffix), slot()}, "", nil},
		{"slot bad path", fits, []*types.Request{slot(), slot("c.json")}, errors.PULSE_MODEL_SAVE_INVALID,
			map[string]any{"path": "c.json", "reason": ReasonSaveSuffix, "regression": 0, "request": 1}},
		{"slot duplicates a fit", fits, []*types.Request{slot("c" + FileSuffix), slot(a)}, errors.PULSE_MODEL_SAVE_INVALID,
			map[string]any{"path": a, "reason": ReasonSaveDuplicate, "sites": []any{"fits[0]", "requests[1].regressions[0]"}}},
		{"slot duplicates a fit request", fits, []*types.Request{slot(b)}, errors.PULSE_MODEL_SAVE_INVALID,
			map[string]any{"path": b, "reason": ReasonSaveDuplicate, "sites": []any{"fits[0].request.regressions[0]", "requests[0].regressions[0]"}}},
		{"two slots", nil, []*types.Request{slot(a), nil, slot(a)}, errors.PULSE_MODEL_SAVE_INVALID,
			map[string]any{"path": a, "reason": ReasonSaveDuplicate, "sites": []any{"requests[0].regressions[0]", "requests[2].regressions[0]"}}},
	})
}
