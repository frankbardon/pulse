package pulse

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/spf13/afero"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// comparison_record_test.go pins the {record: {cohort?, keys}} reference
// of ATTR_PROFILE_SIMILARITY / ATTR_VEC_DISTANCE (U30 E3-S2) at the
// facade, hermetic over fs.NewMemMap: the stored record — from another
// cohort or the request's own — scores exactly as the literal of its
// member values on every arm (serial, shard fan-out, Compose, chain
// stage 0, materialize); every lookup refusal (no index, stale, not
// found, ambiguous, a shard archive) refuses the run with its own
// PULSE_INDEX_* / PULSE_LOOKUP_* code while predict, which never looks
// up, stays clean; the static shape refusals are predict's exactly.

const recordRefCohort = "ref_profiles.pulse"

// recordFacadeFS is vecFacadeFS plus: ref_profiles.pulse (id, a, b, c,
// region; id 9 twice) indexed on id; a two-shard archive of the
// facade cohort; the facade cohort indexed on week; a cohort missing
// member c (indexed on id); and a stale-index cohort.
func recordFacadeFS(t *testing.T) (afero.Fs, *Pulse) {
	t.Helper()
	fsys := vecFacadeFS(t)
	var rows [][]string
	for i := 1; i <= 6; i++ {
		v := vecFacadeRow(i*7 + 3)
		id := i
		if i == 6 {
			id = 9
		}
		rows = append(rows, []string{strconv.Itoa(id), jsonNumber(v[0]), jsonNumber(v[1]), jsonNumber(v[2]), []string{"n", "s"}[i%2]})
	}
	rows = append(rows, []string{"9", "1", "2", "3", "n"})
	createTestPulseFile(t, fsys, recordRefCohort, []string{"id", "a", "b", "c", "region"}, rows)
	createTestPulseFile(t, fsys, "ref_ab.pulse", []string{"id", "a", "b"}, [][]string{{"1", "1.5", "2.5"}, {"2", "3", "4"}})
	createTestPulseFile(t, fsys, "ref_stale.pulse", []string{"id", "a", "b", "c"}, [][]string{{"1", "1.5", "2.5", "3.5"}})
	var s1, s2 [][]string
	for i := 0; i < 90; i++ {
		v := vecFacadeRow(i)
		row := []string{strconv.Itoa(i), jsonNumber(v[0]), jsonNumber(v[1]), jsonNumber(v[2]), []string{"north", "south"}[i%2]}
		if i < 45 {
			s1 = append(s1, row)
		} else {
			s2 = append(s2, row)
		}
	}
	cols := []string{"week", "a", "b", "c", "region"}
	createTestPulseFile(t, fsys, "vec_s1.pulse", cols, s1)
	createTestPulseFile(t, fsys, "vec_s2.pulse", cols, s2)
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := p.CreateShardArchive(ctx, "vec_arch.pulse", []string{"vec_s1.pulse", "vec_s2.pulse"}); err != nil {
		t.Fatal(err)
	}
	for _, ix := range []struct {
		path string
		keys []string
	}{{recordRefCohort, []string{"id"}}, {vecFacadeCohort, []string{"week"}}, {"ref_ab.pulse", []string{"id"}}, {"ref_stale.pulse", []string{"id"}}} {
		if _, err := p.BuildIndex(ctx, ix.path, ix.keys); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := afero.ReadFile(fsys, "ref_stale.pulse")
	if err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fsys, "ref_stale.pulse", append(raw, 0), 0o644); err != nil {
		t.Fatal(err)
	}
	return fsys, p
}

// recordReference spells a {record} reference: cohort "" is omitted.
func recordReference(cohort, field, value string) string {
	c := ""
	if cohort != "" {
		c = `"cohort":{"filename":"` + cohort + `"},`
	}
	return `{"record":{` + c + `"keys":[{"field":"` + field + `","value":"` + value + `"}]}}`
}

// recordScoring scores the facade cohort (or cohort) with typ / metric
// against reference.
func recordScoring(cohort string, typ types.AttributeType, metric, reference string) *Request {
	r := centroidScoring(typ, `{"metric":"`+metric+`","reference":`+reference+`}`)
	r.Cohort = &types.Cohort{Filename: cohort}
	return r
}

// literalOf is the stored record's members as a literal reference (the
// values Lookup returns — the decoded record).
func literalOf(t *testing.T, p *Pulse, cohort, field, value string) string {
	t.Helper()
	res, err := p.Lookup(context.Background(), &LookupRequest{Cohort: &types.Cohort{Filename: cohort}, Field: field, Value: value, ReturnColumns: []string{"a", "b", "c"}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal([]any{res.Rows[0]["a"], res.Rows[0]["b"], res.Rows[0]["c"]})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func respJSON(t *testing.T, r *Response) string {
	t.Helper()
	b, err := json.Marshal(struct {
		Data       any
		Components any
		Warnings   any
	}{r.Data, r.Components, r.Warnings})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestComparisonRecord_ScoresAsLiteral: a record from another cohort, or
// the request's own (cohort omitted), scores exactly as the literal of
// its member values — serial, shard fan-out (1 and 4 workers), a Compose
// slot, a materialized column — and predict, which never looks up, is
// clean.
func TestComparisonRecord_ScoresAsLiteral(t *testing.T) {
	fsys, _ := recordFacadeFS(t)
	ctx := context.Background()
	cohortBytes, err := afero.ReadFile(fsys, vecFacadeCohort)
	if err != nil {
		t.Fatal(err)
	}
	for _, workers := range []int{1, 4} {
		p, err := New(Options{FS: fsys, ShardWorkers: workers, DecodeWorkers: workers})
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range []struct {
			name, cohort, refCohort, field, value string
			typ                                   types.AttributeType
			metric                                string
		}{
			{"cross-cohort similarity", vecFacadeCohort, recordRefCohort, "id", "3", types.ATTR_PROFILE_SIMILARITY, "cosine"},
			{"cross-cohort fitted distance", vecFacadeCohort, recordRefCohort, "id", "2", types.ATTR_VEC_DISTANCE, "mahalanobis"},
			{"own cohort", vecFacadeCohort, "", "week", "17", types.ATTR_VEC_DISTANCE, "euclidean"},
			{"shard archive rows, single-file record", "vec_arch.pulse", recordRefCohort, "id", "4", types.ATTR_VEC_DISTANCE, "euclidean"},
		} {
			t.Run(c.name+"/workers="+strconv.Itoa(workers), func(t *testing.T) {
				lookIn := c.refCohort
				if lookIn == "" {
					lookIn = c.cohort
				}
				rec := recordScoring(c.cohort, c.typ, c.metric, recordReference(c.refCohort, c.field, c.value))
				lit := recordScoring(c.cohort, c.typ, c.metric, literalOf(t, p, lookIn, c.field, c.value))
				want, err := p.Process(ctx, lit)
				if err != nil {
					t.Fatal(err)
				}
				got, err := p.Process(ctx, rec)
				if err != nil {
					t.Fatal(err)
				}
				if g, w := respJSON(t, got), respJSON(t, want); g != w {
					t.Fatalf("record scores\n%s\nliteral scores\n%s", g, w)
				}
				if n, _ := json.Marshal(got.Data[0]["n_s"]); string(n) == "0" || string(n) == "null" {
					t.Fatalf("vacuous: no row scored (%v)", got.Data[0])
				}
				// Compose: the slot resolves its own record.
				cr, err := p.Compose(ctx, &ComposedRequest{Requests: []*Request{rec}})
				if err != nil {
					t.Fatal(err)
				}
				if g, w := respJSON(t, cr.Responses[0]), respJSON(t, want); g != w {
					t.Fatalf("compose slot\n%s\nwant\n%s", g, w)
				}
				if c.cohort == vecFacadeCohort {
					env, err := p.PredictBytes(ctx, cohortBytes, rec)
					if err != nil {
						t.Fatal(err)
					}
					if len(env.Errors) != 0 {
						t.Fatalf("predict errors %+v", env.Errors)
					}
					// Materialize writes the same column (its description
					// names the reference as written, so read the values).
					out := func(r *Request, path string) string {
						r = cloneRecordReq(r)
						r.Aggregations = nil
						if _, err := p.Materialize(ctx, r, path, MaterializeOptions{Overwrite: true}); err != nil {
							t.Fatal(err)
						}
						sum := vecFacadeRequest("m")
						sum.Cohort = &types.Cohort{Filename: path}
						sum.Aggregations = lit.Aggregations
						resp, err := p.Process(ctx, sum)
						if err != nil {
							t.Fatal(err)
						}
						return respJSON(t, resp)
					}
					if g, w := out(rec, "m_rec.pulse"), out(lit, "m_lit.pulse"); g != w {
						t.Fatalf("materialized record column\n%s\nliteral's\n%s", g, w)
					}
				}
			})
		}
	}
}

// TestComparisonRecord_LookupRefusals: every lookup refusal refuses the
// run with its own code (located on the slot), never a fallback, on
// Process and Compose alike; predict does static validation only, so
// it is clean for each.
func TestComparisonRecord_LookupRefusals(t *testing.T) {
	fsys, p := recordFacadeFS(t)
	ctx := context.Background()
	cohortBytes, err := afero.ReadFile(fsys, vecFacadeCohort)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name      string
		reference string
		code      perr.Code
	}{
		{"no index for these keys", recordReference(recordRefCohort, "region", "n"), perr.PULSE_INDEX_MISSING},
		{"not found", recordReference(recordRefCohort, "id", "77"), perr.PULSE_LOOKUP_NOT_FOUND},
		{"ambiguous", recordReference(recordRefCohort, "id", "9"), perr.PULSE_LOOKUP_AMBIGUOUS},
		{"shard archive cohort", recordReference("vec_arch.pulse", "week", "3"), perr.PULSE_INDEX_UNSUPPORTED_SHARDED},
		{"stale index", recordReference("ref_stale.pulse", "id", "1"), perr.PULSE_INDEX_STALE},
		{"member column absent", recordReference("ref_ab.pulse", "id", "1"), perr.PULSE_VECTOR_REFERENCE_MISMATCH},
	} {
		t.Run(c.name, func(t *testing.T) {
			req := recordScoring(vecFacadeCohort, types.ATTR_VEC_DISTANCE, "euclidean", c.reference)
			env, err := p.PredictBytes(ctx, cohortBytes, req)
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) != 0 {
				t.Fatalf("predict looked up: %+v", env.Errors)
			}
			for _, run := range []struct {
				arm string
				err error
			}{
				{"Process", func() error { _, err := p.Process(ctx, req); return err }()},
				{"Compose", func() error {
					_, err := p.Compose(ctx, &ComposedRequest{Requests: []*Request{req}})
					return err
				}()},
			} {
				var ce *perr.CodedError
				if !asCoded(run.err, &ce) || ce.Code != c.code {
					t.Fatalf("%s: %v, want %s", run.arm, run.err, c.code)
				}
				if ce.Details["attribute"] != "s" {
					t.Errorf("%s: details %+v, want the refusal located on slot s", run.arm, ce.Details)
				}
			}
		})
	}
}

// TestComparisonRecord_StaticRefusalParity: the static shape refusals
// predict reports are the runtime's (the shared decode).
func TestComparisonRecord_StaticRefusalParity(t *testing.T) {
	fsys, p := recordFacadeFS(t)
	ctx := context.Background()
	cohortBytes, err := afero.ReadFile(fsys, vecFacadeCohort)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{
		`{"record":{"keys":[]}}`,
		`{"record":{"keys":[{"field":"id","value":3}]}}`,
		`{"record":{"cohort":{"data_dir":"x"},"keys":[{"field":"id","value":"3"}]}}`,
		`{"record":{"keys":[{"field":"id","value":"3"}],"multiplicity":"first"}}`,
	} {
		t.Run(ref, func(t *testing.T) {
			req := recordScoring(vecFacadeCohort, types.ATTR_PROFILE_SIMILARITY, "cosine", ref)
			env, err := p.PredictBytes(ctx, cohortBytes, req)
			if err != nil {
				t.Fatal(err)
			}
			pe := findEnvError(t, env, perr.SERVICE_VALIDATION)
			_, rerr := p.Process(ctx, req)
			var ce *perr.CodedError
			if !asCoded(rerr, &ce) || ce.Code != perr.SERVICE_VALIDATION || ce.Details["reason"] != pe.details["reason"] {
				t.Fatalf("Process: %v, predict %+v", rerr, pe)
			}
		})
	}
}

// cloneRecordReq copies r's top level and attribute list (Materialize
// may write its slots).
func cloneRecordReq(r *Request) *Request {
	c := *r
	c.Attributes = make([]*types.Attribute, len(r.Attributes))
	for i, a := range r.Attributes {
		ac := *a
		c.Attributes[i] = &ac
	}
	return &c
}
