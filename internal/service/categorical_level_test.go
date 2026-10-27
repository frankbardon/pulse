package service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
)

// categorical_level_test.go pins U51 E5-S10: every LEVEL read — a test's
// split_by or contingency axis, a feature encoder, a regression's
// cluster field, a facet filter — refuses a categorical code its
// dictionary has no entry for (ENCODING_INVALID, reason
// categorical_index_out_of_range) instead of reading it as a null and
// silently dropping the row. A test or regression slot fails ALONE
// (its result carries the error, PULSE_SLOT_FAILED warns, every other
// slot survives; Strict fails with the slot's own code); a feature or a
// facet filter fails the run. Fixtures are E5-S8's: written through the
// encoder with valid codes, then one byte of one record patched.
//
// Level-read sites (record.LevelValueChecked / StringValueChecked, each
// erroring on every arm): TEST_T / _Z / _ANOVA / _ANOVA_WELCH /
// _ANOVA_RM (condition, subject) / _BROWN_FORSYTHE / _KRUSKAL / _KS /
// _MANNWHITNEY / _SHAPIRO_WILK / _HOTELLING / _MANOVA / _BOX_M split_by,
// TEST_CHISQ / _FISHER rows + cols, TEST_CHISQ_GOF rows, TEST_PROP_Z
// field + split_by; ATTR_* comparison key_column and centroid by
// (attribute_compare.go); the include-set label predicate
// (member_set.go); FEAT_ONE_HOT, FEAT_FREQUENCY_ENCODE, FEAT_TARGET_ENCODE
// and FEAT_TRAIN_TEST_SPLIT stratify (feature/*); the regression
// cluster / jackknife_by key (regression.LevelFault + clusterKey); every
// extension's extend.Record.StringValue (the root adapter's latch).
// TestLevelReaders_Checked (internal/processing) fails a new unchecked
// read.

// wantSlotFailedLevel asserts se is the slot error of the E5-S10
// refusal on region.
func wantSlotFailedLevel(t *testing.T, se *types.SlotError) {
	t.Helper()
	if se == nil {
		t.Fatal("slot carries no error: the corrupt row was dropped silently")
	}
	if se.Code != string(errors.ENCODING_INVALID) || se.Details["field"] != "region" ||
		se.Details["reason"] != encx.ReasonCategoricalIndexOutOfRange {
		t.Fatalf("slot error = %s %v, want ENCODING_INVALID on region", se.Code, se.Details)
	}
}

// wantSlotFailedWarning asserts resp carries one PULSE_SLOT_FAILED
// warning of kind naming code ENCODING_INVALID.
func wantSlotFailedWarning(t *testing.T, resp *types.Response, kind string) {
	t.Helper()
	for _, w := range resp.Warnings {
		if w.Code == string(errors.PULSE_SLOT_FAILED) && w.Details["slot_kind"] == kind &&
			w.Details["code"] == string(errors.ENCODING_INVALID) {
			return
		}
	}
	t.Fatalf("no PULSE_SLOT_FAILED %s warning in %v", kind, resp.Warnings)
}

// TestCorruptLevel_TestSlotsFailAlone: a TEST_T split_by and a
// TEST_CHISQ goodness-of-fit contingency axis over the corrupt region fail their
// slot alone on the streaming and buffered arms, a large single file
// and an archive (both serial: a row test is not mergeable) — the
// aggregation beside them answers, the clean twin's test answers, a
// grouped run fails the one bucket, and Strict fails the run with the
// slot's code.
//
// Falsified by reverting the split_by read in tTestRow.UpdateRow to
// the unchecked LevelValue (every split_by case answers with region
// read as absent: no error), and by dropping slotfail.From's
// CorruptRecordData admission (the run fails instead of the slot).
func TestCorruptLevel_TestSlotsFailAlone(t *testing.T) {
	t.Parallel()

	const small = 64
	big := int(parallelDecodeRecordThreshold) + 1024
	svc, dir := ciEnv(t, map[string][]byte{
		"cat.pulse":        ciFile(t, small, 5, ciRegionOff, 7),
		"clean.pulse":      ciFile(t, small, -1, 0, 0),
		"big.pulse":        ciFile(t, big, big-3, ciRegionOff, 9),
		"big_clean.pulse":  ciFile(t, big, -1, 0, 0),
		"arch.pulse":       ciArchive(t, small, 7, ciRegionOff, 200),
		"arch_clean.pulse": ciArchive(t, small, -1, 0, 0),
	})
	p := func(name string) string { return filepath.Join(dir, name) }
	tests := map[string]*types.Test{
		"split_by": {Type: types.TEST_T, Field: "x", SplitBy: "region", Label: "t"},
		"contingency": {Type: types.TEST_CHISQ, Rows: "region", Label: "gof",
			Params: json.RawMessage(`{"expected":{"north":0.5,"south":0.5}}`)},
	}
	arms := []struct {
		name          string
		bad, clean    string
		agg           types.AggregationType
		shard, decode int
		arm           observe.Arm
	}{
		{"streaming", "cat.pulse", "clean.pulse", types.AGG_SUM, 1, 1, observe.ArmStreaming},
		{"buffered", "cat.pulse", "clean.pulse", types.AGG_MEDIAN, 1, 1, observe.ArmBuffered},
		// A row test is not mergeable, so the decode / shard workers
		// leave it on the serial arm: an archive streams its shards.
		{"big_streaming", "big.pulse", "big_clean.pulse", types.AGG_SUM, 1, 4, observe.ArmStreaming},
		{"archive_streaming", "arch.pulse", "arch_clean.pulse", types.AGG_SUM, 2, 1, observe.ArmStreaming},
	}
	for family, test := range tests {
		for _, a := range arms {
			t.Run(family+"/"+a.name, func(t *testing.T) {
				svc.SetShardWorkers(a.shard)
				svc.SetDecodeWorkers(a.decode)
				defer svc.SetShardWorkers(0)
				defer svc.SetDecodeWorkers(0)
				req := func(path string) *types.Request {
					tc := *test
					return &types.Request{
						Cohort:       &types.Cohort{Filename: p(path)},
						Aggregations: []*types.Aggregation{{Type: a.agg, Field: "x", Label: "a"}},
						Tests:        []*types.Test{&tc},
					}
				}
				ei := NewExecInfo(time.Now())
				clean, err := svc.Process(WithExecInfo(context.Background(), ei), req(a.clean))
				if err != nil {
					t.Fatalf("clean twin: %v", err)
				}
				if got := ei.Snapshot().Arm; got != a.arm {
					t.Fatalf("clean twin ran arm %q, want %q", got, a.arm)
				}
				if len(clean.Tests) != 1 || clean.Tests[0].Error != nil {
					t.Fatalf("clean twin's test: %+v", clean.Tests)
				}
				resp, err := svc.Process(context.Background(), req(a.bad))
				if err != nil {
					t.Fatalf("a corrupt level failed the run, want the slot alone: %v", err)
				}
				if len(resp.Tests) != 1 {
					t.Fatalf("tests = %d, want 1", len(resp.Tests))
				}
				wantSlotFailedLevel(t, resp.Tests[0].Error)
				wantSlotFailedWarning(t, resp, "tests")
				if len(resp.Data) == 0 || resp.Data[0]["a"] == nil {
					t.Fatalf("the aggregation beside the failed test did not survive: %v", resp.Data)
				}
			})
		}
	}
	t.Run("strict", func(t *testing.T) {
		svc.SetStrict(true)
		defer svc.SetStrict(false)
		_, err := svc.Process(context.Background(), &types.Request{
			Cohort: &types.Cohort{Filename: p("cat.pulse")},
			Tests:  []*types.Test{{Type: types.TEST_T, Field: "x", SplitBy: "region", Label: "t"}},
		})
		wantDictionaryRefusal(t, err, "region", encx.ReasonCategoricalIndexOutOfRange)
	})
	// A grouped run fails the one bucket whose rows carry the code: its
	// result is the sparse-group shape under the refusal's code.
	t.Run("grouped_bucket", func(t *testing.T) {
		resp, err := svc.Process(context.Background(), &types.Request{
			Cohort:       &types.Cohort{Filename: p("cat.pulse")},
			Groups:       []*types.Group{{Type: types.GROUP_RANGE, Field: "x", Interval: 100}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "a"}},
			Tests:        []*types.Test{{Type: types.TEST_T, Field: "x", SplitBy: "region", Label: "t"}},
		})
		if err != nil {
			t.Fatalf("grouped: %v", err)
		}
		found := false
		for _, w := range resp.Warnings {
			if w.Code == string(errors.ENCODING_INVALID) && w.Details["reason"] == encx.ReasonCategoricalIndexOutOfRange {
				found = true
			}
		}
		if !found {
			t.Fatalf("grouped run: no per-group ENCODING_INVALID warning in %v", resp.Warnings)
		}
	})
}

// TestCorruptLevel_RegressionClusterFailsAlone: a REG_OLS whose cluster
// covariance keys rows by the corrupt region fails its slot alone, on
// the buffered fit (the robust covariance forces it) — never reading
// the code as a null cluster (row dropped) nor keying it by the code's
// numeric echo.
//
// Falsified by deleting the LevelFault pre-check in
// FitEachBufferedWithContext (the fit answers, the corrupt row dropped
// listwise).
func TestCorruptLevel_RegressionClusterFailsAlone(t *testing.T) {
	t.Parallel()

	svc, dir := ciEnv(t, map[string][]byte{
		"cat.pulse":   ciFile(t, 64, 5, ciRegionOff, 7),
		"clean.pulse": ciFile(t, 64, -1, 0, 0),
	})
	req := func(name string) *types.Request {
		return &types.Request{
			Cohort:       &types.Cohort{Filename: filepath.Join(dir, name)},
			Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: "x * x", Label: "y"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "a"}},
			Regressions: []*types.RegressionSpec{{
				Type: types.REG_OLS, Name: "m", Target: "y", Predictors: []string{"x"},
				Covariance: &types.RegressionCovariance{Type: types.CovarianceCluster, Cluster: "region"},
			}},
		}
	}
	clean, err := svc.Process(context.Background(), req("clean.pulse"))
	if err != nil {
		t.Fatalf("clean: %v", err)
	}
	if len(clean.Regressions) != 1 || clean.Regressions[0].Error != nil {
		t.Fatalf("clean regression: %+v", clean.Regressions)
	}
	resp, err := svc.Process(context.Background(), req("cat.pulse"))
	if err != nil {
		t.Fatalf("a corrupt cluster level failed the run, want the slot alone: %v", err)
	}
	wantSlotFailedLevel(t, resp.Regressions[0].Error)
	wantSlotFailedWarning(t, resp, "regressions")
}

// TestCorruptLevel_EncodersRefuse: a feature encoder reading the corrupt
// region refuses the run (features have no per-slot failure shape) on
// its buffered Compute and its streaming PrePass / EmitRow arms.
//
// Falsified by discarding the error of oneHot.EmitRow's level read
// (the streaming FEAT_ONE_HOT / AGG_SUM case then answers an all-zero
// row for the corrupt record).
func TestCorruptLevel_EncodersRefuse(t *testing.T) {
	t.Parallel()

	svc, dir := ciEnv(t, map[string][]byte{
		"cat.pulse":   ciFile(t, 64, 5, ciRegionOff, 7),
		"clean.pulse": ciFile(t, 64, -1, 0, 0),
	})
	for _, ft := range []types.FeatureType{types.FEAT_ONE_HOT, types.FEAT_FREQUENCY_ENCODE} {
		for _, agg := range []types.AggregationType{types.AGG_SUM, types.AGG_MEDIAN} {
			t.Run(string(ft)+"/"+string(agg), func(t *testing.T) {
				req := func(name string) *types.Request {
					return &types.Request{
						Cohort:       &types.Cohort{Filename: filepath.Join(dir, name)},
						Features:     []*types.Feature{{Type: ft, Field: "region"}},
						Aggregations: []*types.Aggregation{{Type: agg, Field: "x", Label: "a"}},
					}
				}
				if _, err := svc.Process(context.Background(), req("clean.pulse")); err != nil {
					t.Fatalf("clean: %v", err)
				}
				_, err := svc.Process(context.Background(), req("cat.pulse"))
				wantDictionaryRefusal(t, err, "region", encx.ReasonCategoricalIndexOutOfRange)
			})
		}
	}
}

// TestCorruptLevel_FacetFilterRefuses: a facet filter that refuses a row
// (FILTER_TRUE truthy reads the corrupt region's label) fails the facet
// rather than dropping the row from filtered_records and every tally —
// on the base filter AND on an additive field's scope, which the
// corrupt row reaches only through the scope (the base chain rejected
// it on x first).
//
// Falsified by restoring allPass's `err != nil || !ok → false` for the
// base chain (base_filter answers with the row dropped) and for the
// additive scope (additive_scope answers).
func TestCorruptLevel_FacetFilterRefuses(t *testing.T) {
	t.Parallel()

	svc, dir := ciEnv(t, map[string][]byte{
		"cat.pulse":   ciFile(t, 64, 5, ciRegionOff, 7),
		"clean.pulse": ciFile(t, 64, -1, 0, 0),
	})
	truthy := &types.Filterer{Type: types.FILTER_TRUE, Field: "region", Values: []string{"truthy"}}
	// Row 5's x is 5.5: the range rejects it before the base chain's
	// truthy filter runs; the additive x scope strips the range.
	xRange := &types.Filterer{Type: types.FILTER_RANGE, Field: "x", Values: []string{"8", "100"}}
	cases := map[string]func(string) *types.FacetRequest{
		"base_filter": func(name string) *types.FacetRequest {
			return &types.FacetRequest{Cohort: &types.Cohort{Filename: filepath.Join(dir, name)},
				Fields: []string{"x"}, Filterers: []*types.Filterer{truthy}}
		},
		"additive_scope": func(name string) *types.FacetRequest {
			return &types.FacetRequest{Cohort: &types.Cohort{Filename: filepath.Join(dir, name)},
				Fields: []string{"x"}, AdditiveFields: []string{"x"},
				Filterers: []*types.Filterer{xRange, truthy}}
		},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := svc.FacetSchema(context.Background(), req("clean.pulse")); err != nil {
				t.Fatalf("clean: %v", err)
			}
			_, err := svc.FacetSchema(context.Background(), req("cat.pulse"))
			wantDictionaryRefusal(t, err, "region", encx.ReasonCategoricalIndexOutOfRange)
		})
	}
}
