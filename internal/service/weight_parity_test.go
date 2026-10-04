package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Weighting gates (weighting-descriptive E1-S4,
// .claude/reference/weighting.md "Operation-order exactness rule"):
//
//   - TestWeightUnityParity — an all-1.0 weight column answers
//     BYTE-identically to no weight at all (Data + Components, minus the
//     weight-only keys), for every weight-aware aggregator in every
//     non-crosstab execution mode;
//   - TestWeightFrequencyExpansionParity — integer weights answer like
//     the physically duplicated rows run unweighted (1e-12 relative;
//     exact for counts and for sums of integer data).
//
// Both are operator × execution-mode harnesses. The operator axis is
// the parityOps table, held total over the manifest's `weight_aware`
// operators by the "coverage" subtest: an operator flipped to
// ClassAware (internal/weighting) without a row here fails the gate. A
// new row is all a later story needs — the mode axis derives each
// mode's applicability from the UNWEIGHTED request (an operator that
// never streams is not run on the streaming arms; one that never merges
// not on the parallel arms) and then asserts the WEIGHTED request
// engaged the same arm, so a weight can never silently knock a request
// off its execution path. The crosstab arms join at E3.

// parityOp is one weight-aware aggregator's row.
type parityOp struct {
	op     types.AggregationType
	fields []string        // one slot per field
	params json.RawMessage // slot params, when the operator needs them

	// baseline is the operator the UNWEIGHTED twin runs (default op):
	// AGG_WEIGHTED_MEAN needs a weight, so its twin is AGG_AVERAGE.
	baseline types.AggregationType
	// renameKeys maps a weighted operator-map key to the baseline key it
	// must equal (AGG_WEIGHTED_MEAN's sum_weighted is AGG_AVERAGE's sum).
	renameKeys map[string]string
	// weightOnlyKeys are operator-map keys only a weighted slot emits,
	// beyond the manifest's optional keys (which are stripped already).
	weightOnlyKeys []string

	// expandExact lists the fields whose figure must match the
	// expanded twin EXACTLY (counts; sums over integer data).
	expandExact []string
	// expandSkipKeys are keys of a map-valued Data figure that are a
	// RAW row count by contract and so do not expand (AGG_WELFORD's n
	// stays the contributing-row count; Σw rides sum_weights).
	expandSkipKeys []string
	// scaleNormalized marks an operator whose PROBABILITY weights are
	// rescaled to sum to the contributing row count (AGG_MEDIAN /
	// AGG_PERCENTILE): expansion equivalence is a frequency-weight
	// property for it, and the probability contract is scale invariance
	// (TestWeightProbabilityQuantileScaleInvariance) instead.
	scaleNormalized bool
}

func (o parityOp) baselineOp() types.AggregationType {
	if o.baseline != "" {
		return o.baseline
	}
	return o.op
}

// parityOps: one row per weight-aware aggregator (E1-S3 core, E2-S1
// shape, E2-S2 counting operators).
var parityOps = []parityOp{
	{op: types.AGG_COUNT, fields: []string{"x", "y"}, expandExact: []string{"x", "y"}},
	{op: types.AGG_SUM, fields: []string{"x", "y"}, expandExact: []string{"x"}},
	{op: types.AGG_AVERAGE, fields: []string{"x", "y"}},
	{op: types.AGG_WEIGHTED_MEAN, fields: []string{"x", "y"}, baseline: types.AGG_AVERAGE,
		renameKeys:     map[string]string{"sum_weighted": "sum"},
		weightOnlyKeys: []string{"sum_weights", "n_eff", "weighted_mean", "m2_weighted", "sum_weights_sq", "weighted_variance"}},
	{op: types.AGG_VARIANCE, fields: []string{"x", "y"}},
	{op: types.AGG_STDDEV, fields: []string{"x", "y"}},
	{op: types.AGG_WELFORD, fields: []string{"x", "y"}, expandSkipKeys: []string{"n"}},
	// E2-S1 shape operators: median / percentile / mode answer a data
	// value and mode count a weight sum, so all are exact on expansion
	// (median / percentile under frequency weights only: probability
	// weights are scale-normalized for them).
	{op: types.AGG_MEDIAN, fields: []string{"x", "y"}, expandExact: []string{"x", "y"}, scaleNormalized: true},
	{op: types.AGG_PERCENTILE, fields: []string{"x", "y"}, params: json.RawMessage(`{"percentile":37.5}`), expandExact: []string{"x", "y"}, scaleNormalized: true},
	{op: types.AGG_MODE, fields: []string{"x", "y"}, expandExact: []string{"x", "y"}},
	{op: types.AGG_MODE_COUNT, fields: []string{"x", "y"}, expandExact: []string{"x", "y"}},
	{op: types.AGG_SKEWNESS, fields: []string{"x", "y"}},
	{op: types.AGG_KURTOSIS, fields: []string{"x", "y"}},
	// E2-S2 counting operators: every figure is a sum of integer weights
	// times integer data (or one division of two such sums), so all are
	// exact on expansion except AGG_RATIO, whose denominator y is
	// fractional. AGG_RATIO ignores its Field (x carries the floor).
	{op: types.AGG_FREQUENCY, fields: []string{"x", "y"}, params: json.RawMessage(`{"value":"3"}`), expandExact: []string{"x", "y"}},
	{op: types.AGG_RATIO, fields: []string{"x"}, params: json.RawMessage(`{"numerator_field":"x","denominator_field":"y"}`)},
	{op: types.AGG_SET_FREQUENCY, fields: []string{"s"}, expandExact: []string{"s"}},
	{op: types.AGG_SET_CARDINALITY_SUM, fields: []string{"s"}, expandExact: []string{"s"}},
	{op: types.AGG_SET_CARDINALITY_AVG, fields: []string{"s"}, expandExact: []string{"s"}},
}

// --- fixture ------------------------------------------------------------

func paritySchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
		{Name: "x", Type: encoding.FieldTypeF64, ByteOffset: 4, CsvColumnIdx: 1, Nullable: true},
		{Name: "y", Type: encoding.FieldTypeF64, ByteOffset: 12, CsvColumnIdx: 2, Nullable: true},
		{Name: "g", Type: encoding.FieldTypeU8, ByteOffset: 20, CsvColumnIdx: 3},
		{Name: "w", Type: encoding.FieldTypeF64, ByteOffset: 21, CsvColumnIdx: 4},
		{Name: "s", Type: encoding.FieldTypeSetU8, ByteOffset: 29, CsvColumnIdx: 5, Nullable: true, Dictionary: parityDict()},
	}}
}

// parityDict is the set column's five-member dictionary.
func parityDict() *encoding.Dictionary {
	d := encoding.NewDictionary()
	for _, m := range []string{"a", "b", "c", "d", "e"} {
		if _, err := d.Add(m); err != nil {
			panic(err)
		}
	}
	return d
}

// parityRow is one fixture record. x is integer-valued (exact sums), y
// fractional (operation-order sensitive); both carry nulls. s is a set
// mask over five members (empty on some rows, null on others).
type parityRow struct {
	id           uint32
	x, y, w      float64
	xNull, yNull bool
	g            uint8
	s            uint64
	sNull        bool
}

func parityRows(n int, weight func(i int) float64) []parityRow {
	rows := make([]parityRow, n)
	for i := range rows {
		rows[i] = parityRow{
			id:    uint32(i),
			x:     float64((i*7)%31) - 9,
			xNull: i%9 == 4,
			y:     float64(i%23)*1.37 + 0.11,
			yNull: i%7 == 3,
			g:     uint8(i % 3),
			w:     weight(i),
			s:     uint64((i * 11) % 32),
			sNull: i%13 == 6,
		}
	}
	return rows
}

func unityWeight(int) float64 { return 1 }

// freqWeight: integer weights 0..3 — a zero weight is valid and stands
// for zero copies.
func freqWeight(i int) float64 { return float64((i * 5) % 4) }

// expandRows physically duplicates each row w times (weight reset to 1).
func expandRows(rows []parityRow) []parityRow {
	var out []parityRow
	for _, r := range rows {
		for k := 0; k < int(r.w); k++ {
			c := r
			c.w = 1
			out = append(out, c)
		}
	}
	return out
}

func encodeParityRows(t testing.TB, rows []parityRow) []byte {
	t.Helper()
	recs := make([][]uint64, len(rows))
	for i, r := range rows {
		recs[i] = []uint64{uint64(r.id), math.Float64bits(r.x), math.Float64bits(r.y), uint64(r.g), math.Float64bits(r.w), r.s}
	}
	return writeNullablePulse(t, paritySchema(), recs, func(r, f int) bool {
		return (f == 1 && rows[r].xNull) || (f == 2 && rows[r].yNull) || (f == 5 && rows[r].sNull)
	})
}

// encodeParityArchive splits rows into `shards` contiguous shards.
func encodeParityArchive(t testing.TB, rows []parityRow, shards int) []byte {
	t.Helper()
	var doc, buf bytes.Buffer
	if err := encx.WriteSchemaDoc(&doc, paritySchema(), uint64(len(rows)), uint16(shards)); err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(&buf)
	write := func(name string, b []byte) {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	write(encx.ReservedSchemaName, doc.Bytes())
	per := (len(rows) + shards - 1) / shards
	for i := 0; i < shards; i++ {
		lo, hi := i*per, min((i+1)*per, len(rows))
		write(fmt.Sprintf("s%d.pulse", i), encodeParityRows(t, rows[lo:hi]))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// --- execution modes ------------------------------------------------------

// parityCohortKind selects how a mode stores its cohort.
type parityCohortKind int

const (
	paritySingleFile parityCohortKind = iota // MemMapFs single file
	parityArchive                            // MemMapFs shard archive
	parityOnDisk                             // OS file (mmap: parallel decode needs a real path)
)

// parityMode is one execution arm. shape adds the slots that steer the
// request onto the arm; their labels start with paritySteer and are
// dropped from both sides before comparison (they are not under test,
// and an opted-out slot does not expand).
type parityMode struct {
	name   string
	cohort parityCohortKind
	shape  func(req *types.Request)
	// configure sets the arm's worker knobs on a fresh service.
	configure func(s *Service)
	// applies reports whether the arm is reachable for the UNWEIGHTED
	// request at all; engaged whether a request actually took it.
	applies func(t *testing.T, s *Service, req *types.Request, c *Cohort) bool
	engaged func(t *testing.T, s *Service, req *types.Request, c *Cohort) bool
}

const paritySteer = "steer_"

func streams(req *types.Request) bool {
	return processing.CanStreamRequest(req, paritySchema())
}

func parityModes() []parityMode {
	serial := func(s *Service) { s.SetShardWorkers(1); s.SetDecodeWorkers(1) }
	return []parityMode{
		{
			name:   "buffered",
			cohort: paritySingleFile,
			// AGG_MEDIAN never streams; opted out of the weight.
			shape: func(req *types.Request) {
				req.Aggregations = append(req.Aggregations, &types.Aggregation{
					Type: types.AGG_MEDIAN, Field: "y", Label: paritySteer + "median", Weight: types.NullSlotWeight()})
			},
			configure: serial,
			applies:   func(*testing.T, *Service, *types.Request, *Cohort) bool { return true },
			engaged: func(_ *testing.T, _ *Service, req *types.Request, _ *Cohort) bool {
				return !streams(req)
			},
		},
		{
			name:      "streaming",
			cohort:    paritySingleFile,
			shape:     func(*types.Request) {},
			configure: serial,
			applies:   func(_ *testing.T, _ *Service, req *types.Request, _ *Cohort) bool { return streams(req) },
			engaged:   func(_ *testing.T, _ *Service, req *types.Request, _ *Cohort) bool { return streams(req) },
		},
		{
			name:   "grouped_streaming",
			cohort: paritySingleFile,
			shape: func(req *types.Request) {
				req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}
			},
			configure: serial,
			applies:   func(_ *testing.T, _ *Service, req *types.Request, _ *Cohort) bool { return streams(req) },
			engaged:   func(_ *testing.T, _ *Service, req *types.Request, _ *Cohort) bool { return streams(req) },
		},
		{
			name:   "two_pass",
			cohort: paritySingleFile,
			// ATTR_ZSCORE is two-pass; opted out of the weight.
			shape: func(req *types.Request) {
				req.Attributes = []*types.Attribute{{Type: types.ATTR_ZSCORE, Field: "y", Label: paritySteer + "z", Weight: types.NullSlotWeight()}}
			},
			configure: serial,
			applies:   func(_ *testing.T, _ *Service, req *types.Request, _ *Cohort) bool { return streams(req) },
			engaged:   func(_ *testing.T, _ *Service, req *types.Request, _ *Cohort) bool { return streams(req) },
		},
		{
			name:      "shard_parallel",
			cohort:    parityArchive,
			shape:     func(*types.Request) {},
			configure: func(s *Service) { s.SetShardWorkers(3); s.SetDecodeWorkers(1) },
			applies: func(_ *testing.T, s *Service, req *types.Request, c *Cohort) bool {
				_, ok := s.shouldFanOut(req, c)
				return ok
			},
			engaged: func(_ *testing.T, s *Service, req *types.Request, c *Cohort) bool {
				_, ok := s.shouldFanOut(req, c)
				return ok
			},
		},
		{
			name:      "parallel_decode",
			cohort:    parityOnDisk,
			shape:     func(*types.Request) {},
			configure: func(s *Service) { s.SetShardWorkers(1); s.SetDecodeWorkers(4) },
			applies:   decodeEngages,
			engaged:   decodeEngages,
		},
	}
}

func decodeEngages(t *testing.T, s *Service, req *types.Request, c *Cohort) bool {
	t.Helper()
	n, err := s.CountRecords(context.Background(), c.path)
	if err != nil {
		t.Fatal(err)
	}
	ok, _ := s.canParallelDecode(req, c.Schema(), c, s.DecodeWorkers(), int(n))
	_, fan := shouldFanOutDecode(s.DecodeWorkers(), int(n))
	return ok && fan
}

// parityStore writes the three cohort kinds for one row set: rows
// below the decode threshold are padded by repetition on the OS-file
// kind only, so parallel decode engages.
type parityStore struct {
	mem   *fs.Config
	disk  *fs.Config
	paths map[parityCohortKind]string
}

func newParityStore(t *testing.T, name string, rows func(n int) []parityRow) *parityStore {
	t.Helper()
	small := rows(600)
	mem := fs.NewMemMap()
	if err := afero.WriteFile(mem.Fs(), name+".pulse", encodeParityRows(t, small), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(mem.Fs(), name+"_archive.pulse", encodeParityArchive(t, small, 3), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	osFs := afero.NewOsFs()
	big := dir + "/" + name + ".pulse"
	if err := afero.WriteFile(osFs, big, encodeParityRows(t, rows(parallelDecodeRecordThreshold+4096)), 0o644); err != nil {
		t.Fatal(err)
	}
	disk, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	return &parityStore{mem: mem, disk: disk, paths: map[parityCohortKind]string{
		paritySingleFile: name + ".pulse",
		parityArchive:    name + "_archive.pulse",
		parityOnDisk:     big,
	}}
}

func (p *parityStore) cfg(k parityCohortKind) *fs.Config {
	if k == parityOnDisk {
		return p.disk
	}
	return p.mem
}

// --- weight sources -------------------------------------------------------

// paritySource is how the weight reaches the slots under test.
type paritySource struct {
	name string
	kind types.WeightKind
	// apply weights the request (request / slot sources) or the service
	// (Options.DefaultWeight); returns the default weight it set, if any.
	apply func(req *types.Request, s *Service, spec types.WeightSpec) *types.WeightSpec
}

func paritySources() []paritySource {
	request := func(req *types.Request, _ *Service, spec types.WeightSpec) *types.WeightSpec {
		req.Weight = &spec
		return nil
	}
	return []paritySource{
		{name: "request_probability", kind: types.WeightKindProbability, apply: request},
		{name: "request_frequency", kind: types.WeightKindFrequency, apply: request},
		{name: "slot", kind: types.WeightKindProbability, apply: func(req *types.Request, _ *Service, spec types.WeightSpec) *types.WeightSpec {
			for _, a := range req.Aggregations {
				if !strings.HasPrefix(a.Label, paritySteer) {
					a.Weight = types.SlotWeightOf(spec)
				}
			}
			return nil
		}},
		{name: "options_default", kind: types.WeightKindProbability, apply: func(_ *types.Request, s *Service, spec types.WeightSpec) *types.WeightSpec {
			s.SetDefaultWeight(&spec)
			return &spec
		}},
	}
}

// --- running --------------------------------------------------------------

func parityRequest(path string, row parityOp, op types.AggregationType) *types.Request {
	req := &types.Request{Cohort: &types.Cohort{Filename: path}}
	for _, f := range row.fields {
		req.Aggregations = append(req.Aggregations, &types.Aggregation{
			Type: op, Field: f, Label: string(row.op) + "_" + f, Params: row.params})
	}
	return req
}

// runArm runs one request on one arm, asserting the arm engaged. A nil
// result means the arm does not apply to this operator.
func runArm(t *testing.T, store *parityStore, mode parityMode, path string, row parityOp, op types.AggregationType, src *paritySource) *types.Response {
	t.Helper()
	cfg := store.cfg(mode.cohort)
	svc := New(cfg)
	mode.configure(svc)
	req := parityRequest(path, row, op)
	mode.shape(req)
	var def *types.WeightSpec
	if src != nil {
		def = src.apply(req, svc, types.WeightSpec{Field: "w", Kind: src.kind})
	}
	cohort, err := svc.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if src == nil {
		if !mode.applies(t, svc, req, cohort) {
			return nil
		}
		if !mode.engaged(t, svc, req, cohort) {
			t.Fatalf("the unweighted request does not take the %s arm", mode.name)
		}
	} else if !mode.engaged(t, svc, processing.StampWeights(req, def), cohort) {
		t.Fatalf("the weighted request left the %s arm its unweighted twin runs on", mode.name)
	}
	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("%s: %v", mode.name, err)
	}
	return resp
}

// manifestAware returns the manifest's weight_aware aggregators, keyed
// by name.
func manifestAware() map[string]descriptor.Operator {
	out := map[string]descriptor.Operator{}
	for _, op := range descx.BuildManifest().Components.Aggregators {
		if op.WeightAware {
			out[op.Name] = op
		}
	}
	return out
}

func assertParityCoverage(t *testing.T) {
	t.Helper()
	aware := manifestAware()
	rows := map[string]bool{}
	for _, r := range parityOps {
		rows[string(r.op)] = true
		if _, ok := aware[string(r.op)]; !ok {
			t.Errorf("parityOps row %s: the manifest does not mark it weight_aware — drop the row or fix the class table", r.op)
		}
	}
	for name := range aware {
		if !rows[name] {
			t.Errorf("%s is weight_aware in the manifest but has no parityOps row — add one (weight_parity_test.go)", name)
		}
	}
}

// stripSteer drops the arm-steering slots from a response.
func stripSteer(resp *types.Response) {
	for _, row := range resp.Data {
		for k := range row {
			if strings.HasPrefix(k, paritySteer) {
				delete(row, k)
			}
		}
	}
	if resp.Components == nil {
		return
	}
	var kept []types.AggregationComponents
	for _, a := range resp.Components.Aggregations {
		if !strings.HasPrefix(a.Label, paritySteer) {
			kept = append(kept, a)
		}
	}
	resp.Components.Aggregations = kept
}

// --- TestWeightUnityParity --------------------------------------------------

// TestWeightUnityParity: an all-1.0 weight answers byte-identically to
// no weight, for every weight-aware aggregator × execution arm × weight
// source. The weighted side sheds only what a weight adds — the floor
// keys (pinned to their unity values first: sum_weights = n_eff = n,
// n_weight_invalid = 0), the manifest's optional operator keys and the
// row's declared weight-only keys — and must then marshal to the same
// bytes, so every float matches bit for bit.
func TestWeightUnityParity(t *testing.T) {
	t.Run("coverage", assertParityCoverage)
	aware := manifestAware()
	store := newParityStore(t, "unity", func(n int) []parityRow { return parityRows(n, unityWeight) })
	for _, mode := range parityModes() {
		for _, row := range parityOps {
			t.Run(mode.name+"/"+string(row.op), func(t *testing.T) {
				path := store.paths[mode.cohort]
				base := runArm(t, store, mode, path, row, row.baselineOp(), nil)
				if base == nil {
					t.Logf("%s does not run on the %s arm unweighted; not applicable", row.baselineOp(), mode.name)
					return
				}
				stripSteer(base)
				want := mustMarshal(t, base)
				for _, src := range paritySources() {
					t.Run(src.name, func(t *testing.T) {
						got := runArm(t, store, mode, path, row, row.op, &src)
						stripSteer(got)
						shedUnity(t, got, row, aware[string(row.op)], src.kind)
						if g := mustMarshal(t, got); !bytes.Equal(g, want) {
							t.Errorf("unity weight changed the answer:\n weighted   %s\n unweighted %s", g, want)
						}
					})
				}
			})
		}
	}
}

// shedUnity asserts the weight-only keys carry their unity values and
// removes them.
func shedUnity(t *testing.T, resp *types.Response, row parityOp, op descriptor.Operator, kind types.WeightKind) {
	t.Helper()
	if resp.Components == nil {
		t.Fatal("weighted response has no components")
	}
	optional := map[string]bool{}
	for _, k := range op.ComponentSchema.Keys {
		if k.Optional {
			optional[k.Name] = true
		}
	}
	for _, k := range row.weightOnlyKeys {
		optional[k] = true
	}
	for i := range resp.Components.Aggregations {
		a := &resp.Components.Aggregations[i]
		if a.SumWeights == nil || a.NWeightInvalid == nil {
			t.Fatalf("slot %s: weighted floor missing — the weight did not apply", a.Label)
		}
		if *a.SumWeights != float64(a.N) || *a.NWeightInvalid != 0 {
			t.Errorf("slot %s: unity floor sum_weights=%v n_weight_invalid=%d, want %d / 0", a.Label, *a.SumWeights, *a.NWeightInvalid, a.N)
		}
		switch {
		case kind == types.WeightKindProbability && (a.NEff == nil || *a.NEff != float64(a.N)):
			t.Errorf("slot %s: unity n_eff = %v, want %d", a.Label, deref(a.NEff), a.N)
		case kind == types.WeightKindFrequency && a.NEff != nil:
			t.Errorf("slot %s: n_eff %v under kind frequency", a.Label, *a.NEff)
		}
		a.SumWeights, a.NEff, a.NWeightInvalid = nil, nil, nil
		for k, v := range a.Operator {
			if to, ok := row.renameKeys[k]; ok {
				delete(a.Operator, k)
				a.Operator[to] = v
			}
		}
		for k := range a.Operator {
			if optional[k] {
				delete(a.Operator, k)
			}
		}
	}
}

func deref[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// --- TestWeightFrequencyExpansionParity ----------------------------------

// TestWeightFrequencyExpansionParity: integer weights (0..3) answer
// like the cohort whose rows are physically duplicated w times, run
// unweighted — every figure within 1e-12 relative, counts and sums of
// integer data exactly, and each weighted slot's sum_weights exactly
// the expanded slot's n. Both kinds: a frequency weight is the
// replication count by definition, and the descriptive figures of an
// integer probability weight are the same numbers — except median /
// percentile (scaleNormalized), whose probability weights are rescaled
// to sum to n, so only their frequency arm expands.
func TestWeightFrequencyExpansionParity(t *testing.T) {
	t.Run("coverage", assertParityCoverage)
	weighted := newParityStore(t, "weighted", func(n int) []parityRow { return parityRows(n, freqWeight) })
	expanded := newParityStore(t, "expanded", func(n int) []parityRow { return expandRows(parityRows(n, freqWeight)) })
	kinds := []paritySource{paritySources()[0], paritySources()[1]}
	for _, mode := range parityModes() {
		for _, row := range parityOps {
			t.Run(mode.name+"/"+string(row.op), func(t *testing.T) {
				base := runArm(t, expanded, mode, expanded.paths[mode.cohort], row, row.baselineOp(), nil)
				if base == nil {
					t.Logf("%s does not run on the %s arm unweighted; not applicable", row.baselineOp(), mode.name)
					return
				}
				stripSteer(base)
				for _, src := range kinds {
					t.Run(src.name, func(t *testing.T) {
						if row.scaleNormalized && src.kind == types.WeightKindProbability {
							t.Skip("probability weights are scale-normalized for this operator; see TestWeightProbabilityQuantileScaleInvariance")
						}
						got := runArm(t, weighted, mode, weighted.paths[mode.cohort], row, row.op, &src)
						stripSteer(got)
						assertExpanded(t, got, base, row)
					})
				}
			})
		}
	}
}

// --- TestWeightProbabilityQuantileScaleInvariance -------------------------

// fracWeight: fractional weights in (0, 3], so a normalized cumulative
// weight never sits on an integer boundary and Σw ≠ n.
func fracWeight(i int) float64 { return float64((i*7919)%997+1) / 331.0 }

// TestWeightProbabilityQuantileScaleInvariance: probability weights are
// rescaled to sum to the contributing row count before AGG_MEDIAN /
// AGG_PERCENTILE take a rank, so multiplying every weight by a constant
// — dyadic, non-dyadic, tiny — answers BYTE-identically (Data and the
// operator components) on every arm the operator runs on.
func TestWeightProbabilityQuantileScaleInvariance(t *testing.T) {
	base := newParityStore(t, "base", func(n int) []parityRow { return parityRows(n, fracWeight) })
	scales := map[string]float64{"x1/1024": 1.0 / 1024, "x1/3": 1.0 / 3, "x1e-6": 1e-6, "x7.3": 7.3}
	stores := map[string]*parityStore{}
	for name, c := range scales {
		stores[name] = newParityStore(t, "scaled", func(n int) []parityRow {
			return parityRows(n, func(i int) float64 { return fracWeight(i) * c })
		})
	}
	src := paritySources()[0] // request_probability
	ran := 0
	for _, mode := range parityModes() {
		for _, row := range parityOps {
			if !row.scaleNormalized {
				continue
			}
			t.Run(mode.name+"/"+string(row.op), func(t *testing.T) {
				if runArm(t, base, mode, base.paths[mode.cohort], row, row.op, nil) == nil {
					t.Skipf("%s does not run on the %s arm", row.op, mode.name)
				}
				ran++
				want := runArm(t, base, mode, base.paths[mode.cohort], row, row.op, &src)
				stripSteer(want)
				for name, store := range stores {
					got := runArm(t, store, mode, store.paths[mode.cohort], row, row.op, &src)
					stripSteer(got)
					if g, w := mustMarshal(t, got.Data), mustMarshal(t, want.Data); string(g) != string(w) {
						t.Errorf("%s: data %s, unscaled %s", name, g, w)
					}
					for i := range want.Components.Aggregations {
						g, w := got.Components.Aggregations[i].Operator, want.Components.Aggregations[i].Operator
						if string(mustMarshal(t, g)) != string(mustMarshal(t, w)) {
							t.Errorf("%s slot %d: components %v, unscaled %v", name, i, g, w)
						}
					}
				}
			})
		}
	}
	if ran == 0 {
		t.Fatal("no arm ran a scale-normalized operator: the gate is vacuous")
	}
}

func assertExpanded(t *testing.T, got, want *types.Response, row parityOp) {
	t.Helper()
	if len(got.Data) != len(want.Data) {
		t.Fatalf("%d result rows, expanded %d", len(got.Data), len(want.Data))
	}
	// Compare the WIRE form: a structured figure (AGG_WELFORD's triple)
	// is a Go struct in Data and a JSON object on the wire.
	var gotData, wantData []map[string]any
	if err := json.Unmarshal(mustMarshal(t, got.Data), &gotData); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(mustMarshal(t, want.Data), &wantData); err != nil {
		t.Fatal(err)
	}
	exact := map[string]bool{}
	for _, f := range row.expandExact {
		exact[string(row.op)+"_"+f] = true
	}
	skip := map[string]bool{}
	for _, k := range row.expandSkipKeys {
		skip[k] = true
	}
	for i := range wantData {
		for k, wv := range wantData[i] {
			gv, ok := gotData[i][k]
			if !ok {
				t.Errorf("row %d: %s missing", i, k)
				continue
			}
			compareExpanded(t, fmt.Sprintf("row %d %s", i, k), gv, wv, exact[k], skip)
		}
	}
	ga, wa := got.Components.Aggregations, want.Components.Aggregations
	if len(ga) != len(wa) {
		t.Fatalf("%d component slots, expanded %d", len(ga), len(wa))
	}
	for i := range wa {
		if ga[i].SumWeights == nil {
			t.Fatalf("slot %s: no weighted floor — the weight did not apply", ga[i].Label)
		}
		if *ga[i].SumWeights != float64(wa[i].N) {
			t.Errorf("slot %s: sum_weights %v, expanded n %d", ga[i].Label, *ga[i].SumWeights, wa[i].N)
		}
	}
}

func compareExpanded(t *testing.T, where string, got, want any, exact bool, skip map[string]bool) {
	t.Helper()
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			t.Errorf("%s: %T, expanded %T", where, got, want)
			return
		}
		for k, wv := range w {
			if skip[k] {
				continue
			}
			compareExpanded(t, where+"."+k, g[k], wv, exact, skip)
		}
	default:
		gf, gok := parityFloat(got)
		wf, wok := parityFloat(want)
		if !gok || !wok {
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("%s = %v, expanded %v", where, got, want)
			}
			return
		}
		if exact {
			if gf != wf {
				t.Errorf("%s = %v, expanded %v (must be exact)", where, gf, wf)
			}
			return
		}
		if !parityClose(gf, wf) {
			t.Errorf("%s = %v, expanded %v (rel %g)", where, gf, wf, math.Abs(gf-wf)/math.Abs(wf))
		}
	}
}

func parityFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint64:
		return float64(x), true
	}
	return 0, false
}

func parityClose(a, b float64) bool {
	if b == 0 {
		return math.Abs(a) <= 1e-12
	}
	return math.Abs(a-b) <= 1e-12*math.Abs(b)
}
