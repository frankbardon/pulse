package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// U48 E1-S3: params.reference on the five two-group tests. Groups are
// read in alphabetical order, so the second level is today's implicit
// reference (diff = first − second). Naming the alphabetical-second
// level reproduces today's result (plus the reference echo); naming the
// alphabetical-first level flips every signed figure together — the
// statistic, diff, its interval, the signed effect size, Mann-Whitney's
// u / r / z / rank-biserial — reverses every per-group array (weights
// included) and leaves the p-value alone.

// referenceTests are the two-group tests over the packed_bool level
// cohort, split by cat_grp (levels "0" < "1"). mwFrequencyOnly marks
// the test probability weights are refused on.
var referenceTests = []struct {
	name, body      string
	mwFrequencyOnly bool
}{
	{"TEST_T", `{"type":"TEST_T","field":"x","split_by":"cat_grp"`, false},
	{"TEST_WELCH", `{"type":"TEST_WELCH","field":"x","split_by":"cat_grp"`, false},
	{"TEST_Z_TWO_SAMPLE", `{"type":"TEST_Z_TWO_SAMPLE","field":"x","split_by":"cat_grp"`, false},
	{"TEST_PROP_Z", `{"type":"TEST_PROP_Z","field":"cat_flag","split_by":"cat_grp","params":{"success":"1"$REF}`, false},
	{"TEST_PROP_Z packed_bool split", `{"type":"TEST_PROP_Z","field":"cat_flag","split_by":"pb_grp","params":{"success":"1"$REF}`, false},
	{"TEST_MANN_WHITNEY_U", `{"type":"TEST_MANN_WHITNEY_U","field":"x","split_by":"cat_grp"`, true},
}

// referenceBody closes a referenceTests body with the reference (none
// when ref is "") and the weight clause.
func referenceBody(body, ref, weight string) string {
	refJSON := ""
	if ref != "" {
		refJSON = `"reference":"` + ref + `"`
	}
	if strings.Contains(body, "$REF") {
		if refJSON != "" {
			refJSON = "," + refJSON
		}
		body = strings.Replace(body, "$REF", refJSON, 1)
	} else if refJSON != "" {
		body += `,"params":{` + refJSON + `}`
	}
	if weight != "" {
		body += `,"weight":{"field":"y","kind":"` + weight + `"}`
	}
	return body + `,"label":"ref"}`
}

func runReferenceTest(t *testing.T, p *pulse.Pulse, cohort, body string) *types.TestResult {
	t.Helper()
	var req types.Request
	if err := json.Unmarshal([]byte(`{"tests":[`+body+`]}`), &req); err != nil {
		t.Fatalf("request %s: %v", body, err)
	}
	req.Cohort = &types.Cohort{Filename: cohort}
	resp, err := p.Process(context.Background(), &req)
	if err != nil {
		t.Fatalf("Process %s: %v", body, err)
	}
	if len(resp.Tests) != 1 || resp.Tests[0] == nil {
		t.Fatalf("%s: %d results", body, len(resp.Tests))
	}
	return resp.Tests[0]
}

// resultJSON is r through the wire encoder, decoded to a generic map.
func resultJSON(t *testing.T, r *types.TestResult) map[string]any {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// closeTo is |a − b| within 1e-12 relative (1e-12 absolute near 0): a
// flipped run sums ranks and moments in the other order.
func closeTo(a, b float64) bool {
	return math.Abs(a-b) <= 1e-12*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
}

// flippedDetailsMismatch reports how flipped's details differ from
// base's with the two groups swapped: arrays reversed, the signed keys
// negated, ci_low / ci_high and u_a / u_b, r_a / r_b swapped (the
// interval negated), every other key equal. "" when they match.
func flippedDetailsMismatch(base, flipped map[string]any) string {
	signed := map[string]bool{"diff": true, "z": true}
	swaps := map[string]string{"u_a": "u_b", "u_b": "u_a", "r_a": "r_b", "r_b": "r_a"}
	num := func(v any) float64 { f, _ := v.(float64); return f }
	for k, bv := range base {
		fv, ok := flipped[k]
		if !ok {
			return "flipped lacks " + k
		}
		switch {
		case k == "ci_low" || k == "ci_high":
			other := map[string]string{"ci_low": "ci_high", "ci_high": "ci_low"}[k]
			if !closeTo(num(flipped[other]), -num(bv)) {
				return k + " not mirrored"
			}
		case swaps[k] != "":
			if !closeTo(num(flipped[swaps[k]]), num(bv)) {
				return k + " not swapped"
			}
		case signed[k]:
			if !closeTo(num(fv), -num(bv)) {
				return k + " not negated"
			}
		case k == "effect_size":
			be, fe := bv.(map[string]any), fv.(map[string]any)
			for name, v := range be {
				if !closeTo(num(fe[name]), -num(v)) {
					return "effect_size." + name + " not negated"
				}
			}
		default:
			if ba, isArr := bv.([]any); isArr {
				fa, _ := fv.([]any)
				if len(fa) != len(ba) {
					return k + " length differs"
				}
				for i := range ba {
					if ba[i] != fa[len(fa)-1-i] {
						if bf, okf := ba[i].(float64); !okf || !closeTo(bf, num(fa[len(fa)-1-i])) {
							return k + " not reversed"
						}
					}
				}
				continue
			}
			if bf, okf := bv.(float64); okf {
				if !closeTo(bf, num(fv)) {
					return k + " differs"
				}
				continue
			}
			if bv != fv {
				return k + " differs"
			}
		}
	}
	return ""
}

// TestReference_SignFlipProperty: per test, per weight kind the test
// admits, reference = the alphabetical-second level equals today's
// result (plus the echo) and reference = the alphabetical-first level
// equals today's with every signed figure negated, every per-group
// array reversed and the p-value unchanged.
func TestReference_SignFlipProperty(t *testing.T) {
	t.Parallel()

	p, _, cohort := packedBoolLevelCohort(t)
	for _, tc := range referenceTests {
		weights := []string{"", "frequency", "probability"}
		if tc.mwFrequencyOnly {
			weights = weights[:2]
		}
		for _, w := range weights {
			t.Run(tc.name+"/"+w, func(t *testing.T) {
				base := runReferenceTest(t, p, cohort, referenceBody(tc.body, "", w))
				second := runReferenceTest(t, p, cohort, referenceBody(tc.body, "1", w))
				first := runReferenceTest(t, p, cohort, referenceBody(tc.body, "0", w))
				if _, ok := base.Details["reference"]; ok {
					t.Fatal("reference echoed without params.reference")
				}

				// Alphabetical-second: today's result plus the echo.
				if second.Details["reference"] != "1" {
					t.Fatalf("reference echo %v, want \"1\"", second.Details["reference"])
				}
				bj, sj := resultJSON(t, base), resultJSON(t, second)
				delete(sj["details"].(map[string]any), "reference")
				bb, _ := json.Marshal(bj)
				sb, _ := json.Marshal(sj)
				if string(bb) != string(sb) {
					t.Fatalf("reference = alphabetical-second moved the result:\n base %s\n ref  %s", bb, sb)
				}

				// Alphabetical-first: every signed figure negated.
				if first.Details["reference"] != "0" {
					t.Fatalf("reference echo %v, want \"0\"", first.Details["reference"])
				}
				if first.PValue != base.PValue && !closeTo(first.PValue, base.PValue) {
					t.Fatalf("p moved: %v vs %v", first.PValue, base.PValue)
				}
				if first.DF != base.DF && !closeTo(first.DF, base.DF) {
					t.Fatalf("df moved: %v vs %v", first.DF, base.DF)
				}
				wantStat := -base.Statistic
				if base.Type == types.TEST_MANN_WHITNEY_U {
					wantStat = base.Statistic // u_min carries no direction
				}
				if !closeTo(first.Statistic, wantStat) {
					t.Fatalf("statistic %v, want %v", first.Statistic, wantStat)
				}
				fj := resultJSON(t, first)
				fd := fj["details"].(map[string]any)
				delete(fd, "reference")
				if msg := flippedDetailsMismatch(bj["details"].(map[string]any), fd); msg != "" {
					t.Fatalf("%s\n base    %v\n flipped %v", msg, bj["details"], fd)
				}
				if w != "" {
					if _, ok := fd["sum_weights"]; !ok {
						t.Fatal("weighted run carries no sum_weights")
					}
				}
			})
		}
	}
}

// TestReference_MissingLevelIsRuntimeRefusal: a reference that is a
// dictionary level but neither observed group (the filter keeps two
// other subjects) is the runtime's PULSE_TEST_REFERENCE_MISSING;
// predict, reading no records, accepts the request.
func TestReference_MissingLevelIsRuntimeRefusal(t *testing.T) {
	t.Parallel()

	p, fs, cohort := packedBoolLevelCohort(t)
	for _, test := range []string{
		`{"type":"TEST_T","field":"x","split_by":"subj","params":{"reference":"s5"}}`,
		`{"type":"TEST_WELCH","field":"x","split_by":"subj","params":{"reference":"s5"}}`,
		`{"type":"TEST_Z_TWO_SAMPLE","field":"x","split_by":"subj","params":{"reference":"s5"}}`,
		`{"type":"TEST_MANN_WHITNEY_U","field":"x","split_by":"subj","params":{"reference":"s5"}}`,
		`{"type":"TEST_PROP_Z","field":"cat_flag","split_by":"subj","params":{"success":"1","reference":"s5"}}`,
	} {
		var req types.Request
		if err := json.Unmarshal([]byte(`{"tests":[`+test+`],"filterers":[{"type":"FILTER_INCLUDE","field":"subj","values":["s0","s1"]}]}`), &req); err != nil {
			t.Fatal(err)
		}
		req.Cohort = &types.Cohort{Filename: cohort}
		if env := predictEnvelope(t, p, fs, cohort, &req); len(env.Errors) != 0 {
			t.Errorf("predict refused %s: %+v", test, env.Errors)
		}
		_, err := processOrSlotErr(context.Background(), p, &req)
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_TEST_REFERENCE_MISSING {
			t.Errorf("%s: runtime %v, want PULSE_TEST_REFERENCE_MISSING", test, err)
			continue
		}
		if g, _ := ce.Details["groups"].([]string); len(g) != 2 || g[0] != "s0" || g[1] != "s1" {
			t.Errorf("%s: details.groups %v", test, ce.Details["groups"])
		}
	}
}

// TestReference_ShardArchiveMatchesSingleFile: an ungrouped two-group
// test with a reference reads the same over the shard archive (shard
// workers on, each shard's dictionary its own until the canonical
// remap) as over the single file — the level check at construction and
// the reorder at Finalize see the cohort's levels, never a shard's.
func TestReference_ShardArchiveMatchesSingleFile(t *testing.T) {
	t.Parallel()

	f := newMGFixture(t, pulse.Options{})
	for _, ref := range []string{"F", "M"} {
		for _, test := range []string{
			`{"type":"TEST_T","field":"score","split_by":"sex","params":{"reference":"` + ref + `"},"label":"t"}`,
			`{"type":"TEST_MANN_WHITNEY_U","field":"score","split_by":"sex","params":{"reference":"` + ref + `"},"label":"mw"}`,
			`{"type":"TEST_PROP_Z","field":"tier","split_by":"sex","params":{"success":"gold","reference":"` + ref + `"},"label":"p"}`,
		} {
			run := func(p *pulse.Pulse, cohort string) string {
				t.Helper()
				req := &types.Request{Cohort: &types.Cohort{Filename: cohort}, Tests: gtDecodeTests(t, "["+test+"]")}
				resp, err := p.Process(context.Background(), req)
				if err != nil {
					t.Fatalf("%s on %s: %v", test, cohort, err)
				}
				b, _ := json.Marshal(resp.Tests)
				return string(b)
			}
			single, archive := run(f.p, f.cohort), run(f.shard, f.archive)
			if single != archive {
				t.Errorf("%s:\n single  %s\n archive %s", test, single, archive)
			}
			if !strings.Contains(single, `"reference":"`+ref+`"`) {
				t.Errorf("%s: no reference echo in %s", test, single)
			}
		}
	}
}
