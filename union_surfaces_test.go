package pulse

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// The union on every single-cohort surface past Process (E5-S3): a
// chain's stage 0, a facet, a sample, a materialize, a point lookup and
// a join's LEFT side each read the union exactly as they read the
// shard-archive oracle of the same shards (unionOracleFixture), and
// each one's predict judges it over the same merged schema.

// unionMembers is the oracle fixture's three members as a union.
func unionMembers() []types.UnionMember {
	var out []types.UnionMember
	for m, l := range unionOracleLabels {
		out = append(out, types.UnionMember{Cohort: &types.Cohort{Filename: fmt.Sprintf("m%d.pulse", m)}, Label: l})
	}
	return out
}

// oracleCohort is the oracle archive as a cohort.
func oracleCohort() *types.Cohort { return &types.Cohort{Filename: "oracle.pulse"} }

// wireOf is v's wire form.
func wireOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// unionSurfaceEngine is the oracle fixture plus zones.pulse — a join's
// right side keyed on region — on one engine.
func unionSurfaceEngine(t *testing.T) (*Pulse, afero.Fs) {
	t.Helper()
	fsys := unionOracleFixture(t)
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	buildCohort(t, p, "zones.pulse", encoding.Schema{Fields: []encoding.Field{
		{Name: "region_key", Type: encoding.FieldTypeCategoricalU8, Description: "Sales region the zone row describes"},
		{Name: "zone", Type: encoding.FieldTypeCategoricalU8, Description: "Delivery zone serving the region"},
	}}, CohortBuilderOptions{}, []CohortRow{{"north", "cold"}, {"south", "warm"}, {"east", "warm"}, {"west", "cold"}})
	return p, fsys
}

// A union on a join's left side reads exactly what the oracle archive
// does on the left — the members' remapped categoricals hashed against
// the right side's keys, the source field a column of the joined
// stream — through Process (serial and per-shard parallel), a joined
// crosstab and a Compose slot; predict judges the joined union over
// the merged left schema.
func TestUnion_JoinLeftMatchesOracle(t *testing.T) {
	t.Parallel()
	_, fsys := unionSurfaceEngine(t)
	ctx := context.Background()
	src := types.DefaultUnionSourceField
	join := []*types.JoinSpec{{Right: "zones.pulse", On: []types.OnPair{{LeftField: "region", RightField: "region_key"}}}}
	reqs := map[string]*Request{
		"grouped": {Joins: join,
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: src}, {Type: types.GROUP_CATEGORY, Field: "zone"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "units", Label: "n"}, {Type: types.AGG_SUM, Field: "amount", Label: "s"}}},
		"crosstab": {Joins: join, Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "zone"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: src}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "units", Label: "u"},
		}},
	}
	for _, workers := range []int{1, 4} {
		p, err := New(Options{FS: fsys, ShardWorkers: workers})
		if err != nil {
			t.Fatal(err)
		}
		for name, req := range reqs {
			want, err := p.Process(ctx, oracleOver(req))
			if err != nil {
				t.Fatalf("%s: oracle: %v", name, err)
			}
			got, err := p.Process(ctx, unionOver(req))
			if err != nil {
				t.Fatalf("%s/workers=%d: union: %v", name, workers, err)
			}
			if g, w := comparableJSON(t, got), comparableJSON(t, want); g != w {
				t.Errorf("%s/workers=%d: joined union != oracle\nunion:  %s\noracle: %s", name, workers, g, w)
			}
			if req.Crosstab == nil && len(got.Data) == 0 {
				t.Errorf("%s: no joined rows", name)
			}
			res, err := p.Predict(ctx, unionOver(req))
			if err != nil || !res.Valid {
				t.Fatalf("%s: predict = %+v, %v", name, res, err)
			}
			oracle, err := p.Predict(ctx, oracleOver(req))
			if err != nil {
				t.Fatal(err)
			}
			if res.Streamable != oracle.Streamable || wireOf(t, res.SchemaInfo) != wireOf(t, oracle.SchemaInfo) {
				t.Errorf("%s: union predict %v %s, oracle %v %s", name, res.Streamable, wireOf(t, res.SchemaInfo), oracle.Streamable, wireOf(t, oracle.SchemaInfo))
			}
		}
	}
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	u, o := unionOver(reqs["grouped"]), oracleOver(reqs["grouped"])
	u.Label, o.Label = "union", "oracle"
	composed, err := p.Compose(ctx, &ComposedRequest{Requests: []*Request{u, o}})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	if g, w := wireOf(t, composed.Responses[0].Data), wireOf(t, composed.Responses[1].Data); g != w {
		t.Errorf("Compose joined union slot %s, oracle slot %s", g, w)
	}
}

// A chain's stage 0 reads the union as the oracle archive: every stage
// answers alike, and its predict reports the oracle's stage schemas.
func TestUnion_ChainMatchesOracle(t *testing.T) {
	t.Parallel()
	p, _ := unionSurfaceEngine(t)
	ctx := context.Background()
	src := types.DefaultUnionSourceField
	chain := func() *ChainRequest {
		return &ChainRequest{Stages: []*types.ChainStage{
			{Name: "by_region", Request: &Request{
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: src}, {Type: types.GROUP_CATEGORY, Field: "region"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "units", Label: "u"}},
			}},
			{Name: "by_source", Request: &Request{
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: src}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_MAX, Field: "u", Label: "top"}},
			}},
		}}
	}
	uc, oc := chain(), chain()
	uc.Union, oc.Cohort = unionMembers(), oracleCohort()
	got, err := p.ProcessChain(ctx, uc)
	if err != nil {
		t.Fatalf("ProcessChain(union): %v", err)
	}
	want, err := p.ProcessChain(ctx, oc)
	if err != nil {
		t.Fatalf("ProcessChain(oracle): %v", err)
	}
	for i := range want.Stages {
		if g, w := comparableJSON(t, got.Stages[i]), comparableJSON(t, want.Stages[i]); g != w {
			t.Errorf("stage %d: union %s\noracle %s", i, g, w)
		}
	}
	if len(got.Final.Data) != 3 {
		t.Errorf("final rows %+v, want one per label", got.Final.Data)
	}

	uc, oc = chain(), chain()
	uc.Union, oc.Cohort = unionMembers(), oracleCohort()
	uenv, err := p.PredictChain(ctx, uc)
	if err != nil {
		t.Fatal(err)
	}
	oenv, err := p.PredictChain(ctx, oc)
	if err != nil {
		t.Fatal(err)
	}
	ur, or := uenv.Data.(*ChainPredictResult), oenv.Data.(*ChainPredictResult)
	if !ur.Valid || len(uenv.Errors) > 0 || wireOf(t, ur.StageSchemas) != wireOf(t, or.StageSchemas) || wireOf(t, ur.SchemaInfo) != wireOf(t, or.SchemaInfo) {
		t.Errorf("PredictChain(union) = %v %+v %v, oracle %v", ur.Valid, uenv.Errors, ur.StageSchemas, or.StageSchemas)
	}
}

// A facet over a union counts, filters and summarises exactly what it
// does over the oracle archive — the source field a faceted field like
// any other — and its predict reads the oracle's schema.
func TestUnion_FacetMatchesOracle(t *testing.T) {
	t.Parallel()
	p, _ := unionSurfaceEngine(t)
	ctx := context.Background()
	src := types.DefaultUnionSourceField
	facet := func() *FacetRequest {
		return &FacetRequest{
			Fields:             []string{src, "region", "amount", "units"},
			Filterers:          []*types.Filterer{{Type: types.FILTER_EXCLUDE, Field: "region", Values: []string{"west"}}},
			AdditiveFields:     []string{"region"},
			NumericPercentiles: []float64{0.5},
		}
	}
	uf, of := facet(), facet()
	uf.Union, of.Cohort = unionMembers(), oracleCohort()
	got, err := p.FacetSchema(ctx, uf)
	if err != nil {
		t.Fatalf("FacetSchema(union): %v", err)
	}
	want, err := p.FacetSchema(ctx, of)
	if err != nil {
		t.Fatalf("FacetSchema(oracle): %v", err)
	}
	if g, w := wireOf(t, got), wireOf(t, want); g != w {
		t.Errorf("facet union %s\noracle %s", g, w)
	}
	if got.TotalRecords != 20 || got.Fields[src] == nil {
		t.Errorf("facet union: %d records, fields %v", got.TotalRecords, got.Fields)
	}
	uf, of = facet(), facet()
	uf.Union, of.Cohort = unionMembers(), oracleCohort()
	uenv, err := p.PredictFacet(ctx, uf)
	if err != nil {
		t.Fatal(err)
	}
	oenv, err := p.PredictFacet(ctx, of)
	if err != nil {
		t.Fatal(err)
	}
	ur, or := uenv.Data.(*FacetPredictResult), oenv.Data.(*FacetPredictResult)
	if !ur.Valid || wireOf(t, ur.SchemaInfo) != wireOf(t, or.SchemaInfo) || wireOf(t, uenv.Warnings) != wireOf(t, oenv.Warnings) {
		t.Errorf("PredictFacet(union) = %v %s %+v, oracle %s %+v", ur.Valid, wireOf(t, ur.SchemaInfo), uenv.Warnings, wireOf(t, or.SchemaInfo), oenv.Warnings)
	}
}

// A sample of a union lists the union's records in union order —
// members in declared order — exactly as the oracle archive's sample,
// the source field a column like any other.
func TestUnion_SampleMatchesOracle(t *testing.T) {
	t.Parallel()
	p, _ := unionSurfaceEngine(t)
	ctx := context.Background()
	for _, n := range []int{3, 100} {
		got, err := p.SampleWithRequest(ctx, &SampleRequest{Union: unionMembers(), N: n})
		if err != nil {
			t.Fatalf("SampleWithRequest(union, %d): %v", n, err)
		}
		want, err := p.SampleWithRequest(ctx, &SampleRequest{Cohort: oracleCohort(), N: n})
		if err != nil {
			t.Fatal(err)
		}
		if g, w := wireOf(t, got.Rows), wireOf(t, want.Rows); g != w {
			t.Errorf("sample %d: union %s\noracle %s", n, g, w)
		}
		if len(got.Rows) != min(n, 20) {
			t.Errorf("sample %d: %d rows", n, len(got.Rows))
		}
	}
}

// Materializing a union writes the merged schema — the members' fields
// in the first member's order, then the virtual source field — and the
// oracle archive's rows, the derived columns included; its predict
// names the columns the run writes.
func TestUnion_MaterializeMatchesOracle(t *testing.T) {
	t.Parallel()
	p, _ := unionSurfaceEngine(t)
	ctx := context.Background()
	mat := func() *Request {
		return &Request{
			Features:  []*types.Feature{{Type: types.FEAT_LOG, Field: "amount", Label: "log_amount"}},
			Filterers: []*types.Filterer{{Type: types.FILTER_EXCLUDE, Field: "region", Values: []string{"south"}}},
		}
	}
	u, o := mat(), mat()
	u.Union, o.Cohort = unionMembers(), oracleCohort()
	env, err := p.PredictMaterialize(ctx, u)
	if err != nil || len(env.Errors) > 0 {
		t.Fatalf("PredictMaterialize(union) = %+v, %v", env, err)
	}
	ures, err := p.Materialize(ctx, u, "u_out.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatalf("Materialize(union): %v", err)
	}
	ores, err := p.Materialize(ctx, o, "o_out.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatalf("Materialize(oracle): %v", err)
	}
	if ures.Records != ores.Records || ures.Records == 0 {
		t.Errorf("records: union %d, oracle %d", ures.Records, ores.Records)
	}
	uinfo, err := p.Inspect(ctx, "u_out.pulse")
	if err != nil {
		t.Fatal(err)
	}
	oinfo, err := p.Inspect(ctx, "o_out.pulse")
	if err != nil {
		t.Fatal(err)
	}
	cols := env.Data.(*descriptor.MaterializePredictResult).Columns
	if len(uinfo.Fields) != len(oinfo.Fields) || len(cols) != len(uinfo.Fields) {
		t.Fatalf("union wrote %d fields, oracle %d, predict %d", len(uinfo.Fields), len(oinfo.Fields), len(cols))
	}
	for i, f := range uinfo.Fields {
		of := oinfo.Fields[i]
		if f.Name != of.Name || f.Type != of.Type || f.Nullable != of.Nullable || cols[i].Name != f.Name || cols[i].Type != f.Type {
			t.Errorf("field %d: union %s %s, oracle %s %s, predict %s %s", i, f.Name, f.Type, of.Name, of.Type, cols[i].Name, cols[i].Type)
		}
	}
	if uinfo.Fields[4].Name != types.DefaultUnionSourceField || !strings.HasSuffix(cols[5].Description, "derived from union") {
		t.Errorf("source field / derived column: %+v / %q", uinfo.Fields[4], cols[5].Description)
	}
	all := func(path string) string {
		res, err := p.SampleWithRequest(ctx, &SampleRequest{Cohort: &types.Cohort{Filename: path}, N: 100})
		if err != nil {
			t.Fatal(err)
		}
		return wireOf(t, res.Rows)
	}
	if g, w := all("u_out.pulse"), all("o_out.pulse"); g != w {
		t.Errorf("materialized rows: union %s\noracle %s", g, w)
	}
}

// A point lookup over a union SCANS: it needs no sidecar index and
// writes none, and it returns what the indexed oracle archive returns —
// every multiplicity, a composite key on the source field, the
// not-found and ambiguous refusals.
func TestUnion_LookupScansAsIndexedOracle(t *testing.T) {
	t.Parallel()
	p, fsys := unionSurfaceEngine(t)
	ctx := context.Background()
	src := types.DefaultUnionSourceField
	if _, err := p.BuildIndex(ctx, "oracle.pulse", []string{"units"}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.BuildIndex(ctx, "oracle.pulse", []string{src, "units"}); err != nil {
		t.Fatal(err)
	}
	before, _ := afero.ReadDir(fsys, ".")
	keys := func(kv ...string) []types.LookupKey {
		var out []types.LookupKey
		for i := 0; i+1 < len(kv); i += 2 {
			out = append(out, types.LookupKey{Field: kv[i], Value: kv[i+1]})
		}
		return out
	}
	cases := []struct {
		name string
		keys []types.LookupKey
		mode types.LookupMultiplicity
		code errors.Code
	}{
		{"all", keys("units", "3"), types.LookupMultiplicityAll, ""},
		{"first", keys("units", "3"), types.LookupMultiplicityFirst, ""},
		{"composite", keys(src, "wave2", "units", "3"), "", ""},
		{"ambiguous", keys("units", "3"), "", errors.PULSE_LOOKUP_AMBIGUOUS},
		{"not found", keys("units", "9"), types.LookupMultiplicityAll, errors.PULSE_LOOKUP_NOT_FOUND},
	}
	for _, tc := range cases {
		got, uerr := p.Lookup(ctx, &LookupRequest{Union: unionMembers(), Keys: tc.keys, Multiplicity: tc.mode})
		want, oerr := p.Lookup(ctx, &LookupRequest{Cohort: oracleCohort(), Keys: tc.keys, Multiplicity: tc.mode})
		if tc.code != "" {
			if !errors.HasCode(uerr, tc.code) || !errors.HasCode(oerr, tc.code) {
				t.Errorf("%s: union %v, oracle %v, want %s", tc.name, uerr, oerr, tc.code)
			}
			continue
		}
		if uerr != nil || oerr != nil {
			t.Fatalf("%s: union %v, oracle %v", tc.name, uerr, oerr)
		}
		if g, w := wireOf(t, got), wireOf(t, want); g != w || len(got.Rows) == 0 {
			t.Errorf("%s: union %s\noracle %s", tc.name, g, w)
		}
	}
	after, _ := afero.ReadDir(fsys, ".")
	if len(after) != len(before) {
		t.Errorf("a union lookup wrote files: %d -> %d entries", len(before), len(after))
	}
}

// A comparison slot's {record} reference with no record.cohort reads
// the union request's own records (a scan), exactly as the oracle's
// indexed lookup reads its own.
func TestUnion_RecordReferenceMatchesOracle(t *testing.T) {
	t.Parallel()
	p, _ := unionSurfaceEngine(t)
	ctx := context.Background()
	src := types.DefaultUnionSourceField
	if _, err := p.BuildIndex(ctx, "oracle.pulse", []string{src, "units"}); err != nil {
		t.Fatal(err)
	}
	req := &Request{
		Attributes: []*types.Attribute{{Type: types.ATTR_VEC_DISTANCE, Label: "d", Fields: []string{"amount", "units"},
			Params: json.RawMessage(`{"metric":"euclidean","reference":{"record":{"keys":[{"field":"` + src + `","value":"wave2"},{"field":"units","value":"3"}]}}}`)}},
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: src}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "d", Label: "total_d"}},
	}
	want, err := p.Process(ctx, oracleOver(req))
	if err != nil {
		t.Fatalf("oracle: %v", err)
	}
	got, err := p.Process(ctx, unionOver(req))
	if err != nil {
		t.Fatalf("union: %v", err)
	}
	if g, w := comparableJSON(t, got), comparableJSON(t, want); g != w {
		t.Errorf("record reference: union %s\noracle %s", g, w)
	}
}

// A regression `save` on a union request records each member's cohort
// path and fingerprint in the model file's provenance, in declared
// order; a single-cohort save's provenance is unchanged.
func TestUnion_ModelSaveProvenanceNamesMembers(t *testing.T) {
	t.Parallel()
	p, fsys := unionSurfaceEngine(t)
	ctx := context.Background()
	req := &Request{Union: unionMembers(), Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Name: "m",
		Target: "amount", Predictors: []string{"units"}, Save: "fit" + model.FileSuffix}}}
	if _, err := p.Process(ctx, req); err != nil {
		t.Fatalf("Process: %v", err)
	}
	data, err := afero.ReadFile(fsys, "fit"+model.FileSuffix)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Provenance model.Provenance `json:"provenance"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	prov := file.Provenance
	if prov.Cohort != "" || prov.Fingerprint != "" || prov.RequestDigest == "" || len(prov.Union) != 3 {
		t.Fatalf("provenance %+v", prov)
	}
	for i, m := range prov.Union {
		if m.Label != unionOracleLabels[i] || m.Cohort != "m"+strconv.Itoa(i)+".pulse" || !strings.HasPrefix(m.Fingerprint, "sha256:") {
			t.Errorf("member %d: %+v", i, m)
		}
	}
	if prov.Union[0].Fingerprint == prov.Union[1].Fingerprint {
		t.Error("two members share one fingerprint")
	}
}

// Every union refusal is one rule on every root that reads a union: a
// chain, a facet, a sample, a lookup and a materialize refuse each
// shape and merge refusal with predict's code, and each one's predict
// (chain, facet, materialize) reports the same code.
func TestUnion_RootRefusalsMatchPredict(t *testing.T) {
	t.Parallel()
	p := unionFixture(t)
	ctx := context.Background()
	members := func(pairs ...string) []types.UnionMember {
		var out []types.UnionMember
		for i := 0; i+1 < len(pairs); i += 2 {
			out = append(out, types.UnionMember{Cohort: &types.Cohort{Filename: pairs[i]}, Label: pairs[i+1]})
		}
		return out
	}
	type slots struct {
		cohort *types.Cohort
		union  []types.UnionMember
		field  string
	}
	cases := []struct {
		name string
		s    slots
		code errors.Code
	}{
		{"cohort and union", slots{&types.Cohort{Filename: "a.pulse"}, members("b.pulse", "x"), ""}, errors.PULSE_UNION_COHORT_CONFLICT},
		{"duplicate label", slots{nil, members("a.pulse", "x", "b.pulse", "x"), ""}, errors.PULSE_UNION_LABEL_INVALID},
		{"member without cohort", slots{nil, []types.UnionMember{{Label: "x"}}, ""}, errors.PULSE_UNION_MEMBER_INVALID},
		{"source field without union", slots{&types.Cohort{Filename: "a.pulse"}, nil, "wave"}, errors.PULSE_UNION_SOURCE_FIELD_INVALID},
		{"source field collision", slots{nil, members("a.pulse", "x", "b.pulse", "y"), "amount"}, errors.PULSE_UNION_SOURCE_FIELD_INVALID},
		{"schema mismatch", slots{nil, members("a.pulse", "x", "nullable.pulse", "y"), ""}, errors.PULSE_UNION_SCHEMA_MISMATCH},
	}
	coded := func(err error) errors.Code {
		var ce *errors.CodedError
		if stderrors.As(err, &ce) {
			return ce.Code
		}
		return ""
	}
	envCode := func(env *descriptor.Envelope, err error) errors.Code {
		if err != nil {
			return "error: " + errors.Code(err.Error())
		}
		if len(env.Errors) == 0 {
			return ""
		}
		return errors.Code(env.Errors[0].Code)
	}
	stage := func() []*types.ChainStage {
		return []*types.ChainStage{{Request: &Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "amount", Label: "s"}}}}}
	}
	for _, tc := range cases {
		s := tc.s
		chain := &ChainRequest{Cohort: s.cohort, Union: s.union, UnionSourceField: s.field, Stages: stage()}
		facet := &FacetRequest{Cohort: s.cohort, Union: s.union, UnionSourceField: s.field, Fields: []string{"amount"}}
		mat := &Request{Cohort: s.cohort, Union: s.union, UnionSourceField: s.field, Features: []*types.Feature{{Type: types.FEAT_LOG, Field: "amount"}}}
		got := map[string]errors.Code{}
		_, err := p.ProcessChain(ctx, chain)
		got["chain"] = coded(err)
		got["predict chain"] = envCode(p.PredictChain(ctx, &ChainRequest{Cohort: s.cohort, Union: s.union, UnionSourceField: s.field, Stages: stage()}))
		_, err = p.FacetSchema(ctx, facet)
		got["facet"] = coded(err)
		got["predict facet"] = envCode(p.PredictFacet(ctx, facet))
		_, err = p.SampleWithRequest(ctx, &SampleRequest{Cohort: s.cohort, Union: s.union, UnionSourceField: s.field, N: 2})
		got["sample"] = coded(err)
		_, err = p.Lookup(ctx, &LookupRequest{Cohort: s.cohort, Union: s.union, UnionSourceField: s.field, Field: "amount", Value: "0"})
		got["lookup"] = coded(err)
		_, err = p.Materialize(ctx, mat, "refused.pulse", MaterializeOptions{})
		got["materialize"] = coded(err)
		got["predict materialize"] = envCode(p.PredictMaterialize(ctx, mat))
		for surface, code := range got {
			if code != tc.code {
				t.Errorf("%s: %s = %q, want %s", tc.name, surface, code, tc.code)
			}
		}
	}
	// No union and no source field: every root reads its one cohort,
	// byte-identically — the union gate is silent.
	if err := descx.UnionShapeRefusalOf(&types.Cohort{Filename: "a.pulse"}, nil, ""); err != nil {
		t.Errorf("a plain cohort refused: %v", err)
	}
}

// Explain checks a union root as its predict does: a request, a chain
// and a facet over a union are predicted over the merged schema (no
// "not checked" caveat, the inferred operator named), and a refused
// union is explained with its code.
func TestUnion_ExplainChecksUnionRoots(t *testing.T) {
	t.Parallel()
	p, _ := unionSurfaceEngine(t)
	ctx := context.Background()
	src := types.DefaultUnionSourceField
	untyped := func() *Request {
		return &Request{Union: unionMembers(),
			Groups:       []*types.Group{{Field: src}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "units", Label: "u"}}}
	}
	check := func(name string, req descriptor.ExplainRequest, valid bool, code string) {
		t.Helper()
		res, err := p.Explain(ctx, req)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.Valid == nil || *res.Valid != valid {
			t.Fatalf("%s: valid %v, want %v (caveats %v)", name, res.Valid, valid, res.Caveats)
		}
		for _, c := range res.Caveats {
			if strings.Contains(c, "not checked") {
				t.Errorf("%s: unchecked caveat %q", name, c)
			}
		}
		if code != "" && (len(res.Refusals) == 0 || res.Refusals[0].Code != code) {
			t.Errorf("%s: refusals %+v, want %s", name, res.Refusals, code)
		}
	}
	req := untyped()
	check("request", descriptor.ExplainRequest{Request: req}, true, "")
	res, err := p.Explain(ctx, descriptor.ExplainRequest{Request: untyped()})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.DefaultsApplied) == 0 || !strings.Contains(res.Summary, "a union of 3 cohorts") {
		t.Errorf("request: defaults %+v, summary %q", res.DefaultsApplied, res.Summary)
	}
	check("chain", descriptor.ExplainRequest{Chain: &ChainRequest{Union: unionMembers(),
		Stages: []*types.ChainStage{{Request: &Request{Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: src}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "units", Label: "u"}}}}}}}, true, "")
	check("facet", descriptor.ExplainRequest{Facet: &FacetRequest{Union: unionMembers(), Fields: []string{src}}}, true, "")
	dup := untyped()
	dup.Union[1].Label = dup.Union[0].Label
	check("refused", descriptor.ExplainRequest{Request: dup}, false, string(errors.PULSE_UNION_LABEL_INVALID))
}
