// Package slotfail is the per-slot partial-success rule shared by the
// engine (internal/processing), which turns a slot's data-dependent
// runtime refusal into a result carrying a types.SlotError, and the
// orchestrator (internal/service), which lifts each failed slot to a
// PULSE_SLOT_FAILED warning — or, under Options.Strict, fails the run
// with the slot's own code.
//
// Only a DATA-dependent refusal is per-slot: one the request could not
// have foreseen from the schema alone (a separated or unconverged fit, a
// rank-deficient design, too few rows, zero variance, a singular
// matrix). A configuration fault — an unknown type, a bad param, a field
// of the wrong type — is never in a set below and stays fatal at
// validation. The sets are closed: a new data-dependent refusal code
// joins the matching set explicitly. One refusal is admitted to every
// kind beside its set: corrupt record data (CorruptRecordData).
package slotfail

import (
	stderrors "errors"
	"fmt"
	"maps"

	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/types"
)

// Kind names the response slot a failed result sits in: the JSON key of
// its Response slice.
type Kind string

// The per-slot kinds: the four Response slots, a Compose `optimize`
// block (its ComposedResponse.Optimizations entry) and a Compose
// `compare` entry (its ComposedResponse.Comparisons entry).
const (
	KindRegressions Kind = "regressions"
	KindTests       Kind = "tests"
	KindPostTests   Kind = "post_tests"
	KindMatrices    Kind = "matrices"
	// KindOptimizations is a Compose optimize block that failed on its
	// numbers (OptimizeCodes, or a read of a Compose fit that failed on
	// its data — the fit's own code). internal/optsolve builds its
	// error; Collect never lists it (it is no Response slot).
	KindOptimizations Kind = "optimizations"
	// KindComparisons is a Compose nested-model comparison that failed
	// on its data (ComparisonCodes, or a read of a Compose fit that
	// failed on its data — the fit's own code). internal/regcompare
	// builds its error; Collect never lists it (it is no Response slot).
	KindComparisons Kind = "comparisons"
)

// ComparisonCodes are the data-dependent refusals a nested-model
// comparison raises itself: the two fits used different rows.
var ComparisonCodes = map[errors.Code]bool{
	errors.PULSE_COMPARISON_ROWS_DIFFER: true,
}

// OptimizeCodes are the data-dependent refusals an optimize block's
// solve raises: no start converged, or the constraints could not be
// met.
var OptimizeCodes = map[errors.Code]bool{
	errors.PULSE_OPTIMIZE_NOT_CONVERGED: true,
	errors.PULSE_OPTIMIZE_INFEASIBLE:    true,
}

// RegressionCodes are the data-dependent refusals a regression engine's
// fit raises.
var RegressionCodes = map[errors.Code]bool{
	errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA: true,
	errors.PROCESSING_REGRESSION_RANK_DEFICIENT:    true,
	errors.PROCESSING_REGRESSION_NO_CONVERGE:       true,
	errors.PROCESSING_REGRESSION_SEPARATION:        true,
	errors.PROCESSING_REGRESSION_SINGULAR_GRAM:     true,
}

// TestCodes are the data-dependent refusals a test's Finalize / Run
// raises: a single split level, too few rows, zero variance, a
// degenerate table, an undefined correlation, a split missing
// params.reference, a singular or too-small covariance. A grouped run's
// one sparse group turns the same codes into a null-statistics result
// for that group (processing's sparseTestCodes is this set).
var TestCodes = map[errors.Code]bool{
	errors.PULSE_TEST_SPLIT_GROUPS_LT_2:      true,
	errors.PULSE_TEST_INSUFFICIENT_N:         true,
	errors.PULSE_TEST_VARIANCE_ZERO:          true,
	errors.PULSE_TEST_CONTINGENCY_DEGENERATE: true,
	errors.PULSE_TEST_CORRELATION_UNDEFINED:  true,
	errors.PULSE_TEST_REFERENCE_MISSING:      true,
	errors.PULSE_MATRIX_SINGULAR:             true,
	errors.PULSE_MATRIX_INSUFFICIENT_N:       true,
}

// MatrixCodes are the data-dependent refusals a matrix finalizer
// raises.
var MatrixCodes = map[errors.Code]bool{
	errors.PULSE_MATRIX_SINGULAR:       true,
	errors.PULSE_MATRIX_NOT_PSD:        true,
	errors.PULSE_MATRIX_INSUFFICIENT_N: true,
	errors.PULSE_MATRIX_ZERO_VARIANCE:  true,
	errors.PULSE_MATRIX_NOT_CONVERGED:  true,
	errors.PULSE_MATRIX_HEYWOOD:        true,
	// a derived group without params.group_levels whose rows hold more
	// levels than the per-level state cap
	errors.PULSE_MATRIX_GROUP_LEVEL_LIMIT: true,
	// an ordered SEM member whose rows hold more distinct values than
	// sem.MaxOrderedCategories
	errors.PULSE_MATRIX_TOO_MANY_CATEGORIES: true,
}

// CorruptRecordData reports whether coded is the dictionary-index
// refusal of corrupt record data (internal/encoding.CategoricalIndexFault
// / SetMemberFault: ENCODING_INVALID with details.reason
// categorical_index_out_of_range or set_member_out_of_range). It is
// data-dependent on every kind — no schema foresees a corrupt code — so
// a slot whose rows carry one fails alone (U51 E5-S10). Any other
// ENCODING_INVALID stays fatal.
func CorruptRecordData(coded *errors.CodedError) bool {
	if coded == nil || coded.Code != errors.ENCODING_INVALID {
		return false
	}
	switch coded.Details["reason"] {
	case encx.ReasonCategoricalIndexOutOfRange, encx.ReasonSetMemberOutOfRange:
		return true
	}
	return false
}

// codesOf is kind's closed set.
func codesOf(kind Kind) map[errors.Code]bool {
	switch kind {
	case KindRegressions:
		return RegressionCodes
	case KindTests, KindPostTests:
		return TestCodes
	case KindMatrices:
		return MatrixCodes
	case KindOptimizations:
		return OptimizeCodes
	case KindComparisons:
		return ComparisonCodes
	}
	return nil
}

// From is err as a per-slot failure of a kind slot: the SlotError
// carrying the refusal's own code, message and (copied) details, and
// true — or nil, false when err is not a coded data-dependent refusal of
// that kind, so the caller returns it as the fatal error it is.
func From(kind Kind, err error) (*types.SlotError, bool) {
	var coded *errors.CodedError
	if err == nil || !stderrors.As(err, &coded) {
		return nil, false
	}
	if !codesOf(kind)[coded.Code] && (codesOf(kind) == nil || !CorruptRecordData(coded)) {
		return nil, false
	}
	se := &types.SlotError{Code: string(coded.Code), Message: coded.Message}
	if len(coded.Details) > 0 {
		se.Details = maps.Clone(coded.Details)
	}
	return se, true
}

// AsError is se back as the coded error it was raised as: its own code,
// message and details. The Strict refusal and a Compose fit that needs
// the fitted model (which a failed fit does not have) raise it.
func AsError(se *types.SlotError) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.Code(se.Code), se.Message, se.Details)
}

// Failure is one failed slot of a response: its kind, request slot
// index, label, the group key of a grouped matrix bucket (nil
// otherwise) and its SlotError.
type Failure struct {
	Kind     Kind
	Index    int
	Label    string
	GroupKey types.AxisKey
	Err      *types.SlotError
}

// Collect lists every failed slot of resp in response order — tests,
// post-tests, regressions, then matrices — each with the request slot
// index it answers (req is the request resp answers).
func Collect(req *types.Request, resp *types.Response) []Failure {
	if req == nil || resp == nil {
		return nil
	}
	var out []Failure
	tests := func(kind Kind, specs []*types.Test, results []*types.TestResult) {
		for k, r := range results {
			if r == nil || r.Error == nil {
				continue
			}
			out = append(out, Failure{Kind: kind, Index: specIndex(k, len(specs), len(results)), Label: r.Label, GroupKey: r.GroupKey, Err: r.Error})
		}
	}
	tests(KindTests, req.Tests, resp.Tests)
	tests(KindPostTests, req.PostTests, resp.PostTests)
	for i, r := range resp.Regressions {
		if r == nil || r.Error == nil {
			continue
		}
		label := r.Name
		if label == "" {
			label = fmt.Sprintf("regressions[%d]", i)
		}
		out = append(out, Failure{Kind: KindRegressions, Index: i, Label: label, Err: r.Error})
	}
	for k := range resp.Matrices {
		r := &resp.Matrices[k]
		if r.Error == nil {
			continue
		}
		idx := k
		for i := range req.Matrices {
			if req.Matrices[i].Name == r.Name {
				idx = i
				break
			}
		}
		out = append(out, Failure{Kind: KindMatrices, Index: idx, Label: r.Name, GroupKey: r.GroupKey, Err: r.Error})
	}
	return out
}

// specIndex is the request slot index of flat result k: results is
// specs x groups test-major on a grouped run (entry k belongs to spec
// k / groups), one per spec otherwise.
func specIndex(k, specs, results int) int {
	if specs <= 0 || results%specs != 0 {
		return k
	}
	return k / (results / specs)
}

// Warning is f's PULSE_SLOT_FAILED response warning: details
// {slot_kind, index, label, code} (+ group_key for a grouped bucket).
func Warning(f Failure) *types.ResponseWarning {
	details := map[string]any{
		"slot_kind": string(f.Kind),
		"index":     f.Index,
		"label":     f.Label,
		"code":      f.Err.Code,
	}
	where := ""
	if len(f.GroupKey) > 0 {
		details["group_key"] = f.GroupKey
		where = fmt.Sprintf(" in group %v", []any(f.GroupKey))
	}
	return &types.ResponseWarning{
		Code: string(errors.PULSE_SLOT_FAILED),
		Message: fmt.Sprintf("%s[%d] %q failed%s with %s: %s; its result carries the error and the other slots proceed",
			f.Kind, f.Index, f.Label, where, f.Err.Code, f.Err.Message),
		Details: details,
	}
}

// StrictError is f as the Strict refusal: the slot's own code and
// message, its details plus {slot_kind, index, label} (+ group_key).
func StrictError(f Failure) *errors.CodedError {
	ce := AsError(f.Err)
	if ce.Details == nil {
		ce.Details = map[string]any{}
	}
	ce.Details["slot_kind"] = string(f.Kind)
	ce.Details["index"] = f.Index
	ce.Details["label"] = f.Label
	if len(f.GroupKey) > 0 {
		ce.Details["group_key"] = f.GroupKey
	}
	return ce
}

// Apply lifts every failed slot of resp (answering req) into a
// PULSE_SLOT_FAILED warning appended to resp.Warnings, in response
// order. Under strict it instead returns the first failed slot's
// StrictError and leaves resp untouched.
func Apply(req *types.Request, resp *types.Response, strict bool) error {
	fails := Collect(req, resp)
	if len(fails) == 0 {
		return nil
	}
	if strict {
		return StrictError(fails[0])
	}
	for _, f := range fails {
		resp.Warnings = append(resp.Warnings, Warning(f))
	}
	return nil
}

// Scope rewrites every failed slot's error of resp through scope — the
// instance's refusal scoping (remedy prose naming only operators the
// instance offers), which a fatal refusal gets on its way out of the
// service — so a per-slot refusal names no hidden feature either. A nil
// scope leaves resp untouched.
func Scope(resp *types.Response, scope func(error) error) {
	if resp == nil || scope == nil {
		return
	}
	rewrite := func(se *types.SlotError) {
		if se == nil {
			return
		}
		var ce *errors.CodedError
		if !stderrors.As(scope(AsError(se)), &ce) {
			return
		}
		se.Message = ce.Message
		if len(ce.Details) > 0 {
			se.Details = ce.Details
		}
	}
	for _, rs := range [][]*types.TestResult{resp.Tests, resp.PostTests} {
		for _, r := range rs {
			if r != nil {
				rewrite(r.Error)
			}
		}
	}
	for _, r := range resp.Regressions {
		if r != nil {
			rewrite(r.Error)
		}
	}
	for i := range resp.Matrices {
		rewrite(resp.Matrices[i].Error)
	}
}
