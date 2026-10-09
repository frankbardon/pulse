package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/mcp"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// manyOneSampleTests is a request over limitsCohort carrying ten
// one-sample TEST_T slots (ten uncorrected p-values, no multiplicity
// block) — it fires PULSE_ADVISORY_MANY_TESTS and runs cleanly.
func manyOneSampleTests(cohort string) *types.Request {
	req := &types.Request{
		Cohort:       &types.Cohort{Filename: cohort},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "a", Label: "s"}},
	}
	for i := range descriptor.MultiplicityTriggerThreshold {
		req.Tests = append(req.Tests, &types.Test{Type: types.TEST_T, Field: "a", Label: "t" + strconv.Itoa(i)})
	}
	return req
}

func advisoryCodes(r *descriptor.PredictResult) []string {
	var out []string
	for _, a := range r.Advisories {
		out = append(out, a.Code)
	}
	return out
}

func hasAdvisory(r *descriptor.PredictResult, code errors.Code) bool {
	for _, a := range r.Advisories {
		if a.Code == string(code) {
			return true
		}
	}
	return false
}

// TestNew_SuppressAdvisoriesUnknownIsCoded: an unknown code fails New
// with PULSE_SUPPRESS_ADVISORY_UNKNOWN; registered codes are accepted.
func TestNew_SuppressAdvisoriesUnknownIsCoded(t *testing.T) {
	_, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), SuppressAdvisories: []string{"PULSE_ADVISORY_NOPE"}})
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_SUPPRESS_ADVISORY_UNKNOWN {
		t.Fatalf("New(unknown code) err = %v, want %s", err, errors.PULSE_SUPPRESS_ADVISORY_UNKNOWN)
	}
	if _, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), SuppressAdvisories: []string{
		string(errors.PULSE_ADVISORY_MANY_TESTS), string(errors.PULSE_ADVISORY_TWO_GROUP_TEST_MANY_GROUPS),
	}}); err != nil {
		t.Fatalf("New(registered codes): %v", err)
	}
}

// TestPredict_AdvisoriesReachTheFacade: Pulse.Predict (the path MCP
// pulse_predict takes) carries both advisories on the result itself,
// and Options.SuppressAdvisories drops exactly the suppressed code.
func TestPredict_AdvisoriesReachTheFacade(t *testing.T) {
	fs, cohort := limitsCohort(t)
	req := manyOneSampleTests(cohort)
	req.Tests = append(req.Tests, &types.Test{Type: types.TEST_T, Field: "a", SplitBy: "cat", Label: "two"})
	ctx := context.Background()

	open, err := limitsPulse(t, fs, pulse.Limits{}).Predict(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !hasAdvisory(open, errors.PULSE_ADVISORY_MANY_TESTS) || !hasAdvisory(open, errors.PULSE_ADVISORY_TWO_GROUP_TEST_MANY_GROUPS) {
		t.Fatalf("advisories = %v, want both codes", advisoryCodes(open))
	}

	p, err := pulse.New(pulse.Options{FS: fs, SuppressAdvisories: []string{string(errors.PULSE_ADVISORY_MANY_TESTS)}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Predict(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if hasAdvisory(got, errors.PULSE_ADVISORY_MANY_TESTS) || !hasAdvisory(got, errors.PULSE_ADVISORY_TWO_GROUP_TEST_MANY_GROUPS) {
		t.Errorf("suppressed: advisories = %v, want only the two-group code", advisoryCodes(got))
	}
}

// TestPredictBytes_StrictNeverEscalatesAdvisories: under Options.Strict
// an advisory stays an advisory — never an error, never a warning.
func TestPredictBytes_StrictNeverEscalatesAdvisories(t *testing.T) {
	fs, cohort := limitsCohort(t)
	data, err := afero.ReadFile(fs, cohort)
	if err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{FS: fs, Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	env, err := p.PredictBytes(context.Background(), data, manyOneSampleTests(cohort))
	if err != nil {
		t.Fatal(err)
	}
	res := env.Data.(*descriptor.PredictResult)
	if !hasAdvisory(res, errors.PULSE_ADVISORY_MANY_TESTS) {
		t.Fatalf("vacuous: advisories = %v", advisoryCodes(res))
	}
	// Strict promotes the cohort's own description warnings (CSV
	// import leaves fields undescribed); no advisory may join them.
	for _, code := range []errors.Code{errors.PULSE_ADVISORY_MANY_TESTS, errors.PULSE_ADVISORY_TWO_GROUP_TEST_MANY_GROUPS} {
		if envHasCode(env.Errors, string(code)) {
			t.Errorf("strict: advisory %s escalated into errors %+v", code, env.Errors)
		}
	}
	if envHasCode(env.Warnings, string(errors.PULSE_ADVISORY_MANY_TESTS)) {
		t.Errorf("strict: advisory leaked into warnings %+v", env.Warnings)
	}
}

// TestAdvisories_NeverChangeExecution: a request that fires an advisory
// processes byte-identically on an instance suppressing it and on one
// that does not.
func TestAdvisories_NeverChangeExecution(t *testing.T) {
	fs, cohort := limitsCohort(t)
	ctx := context.Background()
	run := func(opts pulse.Options) []byte {
		t.Helper()
		opts.FS = fs
		p, err := pulse.New(opts)
		if err != nil {
			t.Fatal(err)
		}
		res, err := p.Process(ctx, manyOneSampleTests(cohort))
		if err != nil {
			t.Fatalf("Process: %v", err)
		}
		b, err := json.Marshal(res)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	pr, err := limitsPulse(t, fs, pulse.Limits{}).Predict(ctx, manyOneSampleTests(cohort))
	if err != nil {
		t.Fatal(err)
	}
	if !hasAdvisory(pr, errors.PULSE_ADVISORY_MANY_TESTS) {
		t.Fatalf("vacuous: the request fires no advisory (%v)", advisoryCodes(pr))
	}
	plain := run(pulse.Options{})
	suppressed := run(pulse.Options{SuppressAdvisories: []string{string(errors.PULSE_ADVISORY_MANY_TESTS)}})
	if string(plain) != string(suppressed) {
		t.Errorf("Process output differs with the advisory suppressed:\n%s\n---\n%s", plain, suppressed)
	}
}

// TestHandlePredict_CarriesAdvisories: MCP pulse_predict returns the
// PredictResult itself (envelope warnings never reach it), so the
// advisories slot must — and does — ride the tool output.
func TestHandlePredict_CarriesAdvisories(t *testing.T) {
	fs, cohort := limitsCohort(t)
	out, err := mcp.HandlePredict(context.Background(), limitsPulse(t, fs, pulse.Limits{}), *manyOneSampleTests(cohort))
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Advisories []descriptor.Advisory `json:"advisories"`
	}
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.Advisories) != 1 || wire.Advisories[0].Code != string(errors.PULSE_ADVISORY_MANY_TESTS) {
		t.Errorf("pulse_predict advisories = %+v, want one %s", wire.Advisories, errors.PULSE_ADVISORY_MANY_TESTS)
	}
}
