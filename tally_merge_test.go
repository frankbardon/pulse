package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/facadebridge"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Tally attributes on the parallel arms (U51 E3-S2, #456): an
// ATTR_SCALE_SCORE, a reference-form ATTR_PROFILE_SIMILARITY and an
// extension attribute declaring Mergeable keep a per-run tally (their
// Components.Attributes counters and Response warnings). The reducers
// score each partition on its own instance and fold the tallies
// (processing.MergeableAttributeTally / extend.MergeableAttribute), so
// the parallel answer — rows, Components.Attributes, warnings — equals
// the serial one, with each warning raised once.

const (
	attrTallyMerge = types.AttributeType("ATTR_TALLY_HIGH")
	attrTallySolo  = types.AttributeType("ATTR_TALLY_SOLO")
)

// mergeableHighAttr is highAttr with the tally fold.
type mergeableHighAttr struct{ highAttr }

func (a *mergeableHighAttr) MergeTally(other extend.RowLocalAttribute) error {
	o, ok := other.(*mergeableHighAttr)
	if !ok {
		return fmt.Errorf("partner %T", other)
	}
	a.high += o.high
	return nil
}

// tallyRegistration is a component-emitting row-local attribute
// counting rows whose score is above 25; mergeable declares (and
// implements) the tally fold.
func tallyRegistration(name types.AttributeType, mergeable bool) pulse.AttributeRegistration {
	return pulse.AttributeRegistration{
		Name: name,
		Factory: func(*types.Attribute, *encoding.Schema) (extend.AttributeComputer, error) {
			return &mergeableHighAttr{}, nil
		},
		Mode:            pulse.AttributeModeRowLocal,
		Accepts:         []encoding.FieldType{encoding.FieldTypeF64},
		ComponentSchema: highSchema(),
		ComponentsFunc: func(inst extend.AttributeComputer) (map[string]any, error) {
			return map[string]any{"n_high": inst.(*mergeableHighAttr).high}, nil
		},
		Mergeable: mergeable,
	}
}

// tallyExtensions registers the reference table (west has no profile,
// so the keyed slot misses a quarter of the rows) and the two
// extension attributes.
func tallyExtensions() pulse.Extensions {
	return pulse.Extensions{
		ReferenceVectors: map[string]types.ReferenceVectorTable{
			"targets": {Labels: []string{"q", "s"}, Profiles: map[string][]float64{"north": {2, 10}, "south": {6, 30}, "east": {1, 5}}},
		},
		Attributes: []pulse.AttributeRegistration{
			tallyRegistration(attrTallyMerge, true),
			tallyRegistration(attrTallySolo, false),
		},
	}
}

// tallyRequest carries every tally the gate admits: the scale score
// (score above 20 is out of range), the keyed profile similarity (west
// misses), a jaccard one over non-0/1 values (domain misses) and the
// mergeable extension.
func tallyRequest(path string, groups bool) *types.Request {
	req := &types.Request{
		Cohort:  &types.Cohort{Filename: path},
		Vectors: []types.VectorSpec{{Name: "v", Fields: []string{"score", "qty"}, Labels: []string{"s", "q"}}},
		Attributes: []*types.Attribute{
			{Type: types.ATTR_SCALE_SCORE, Fields: []string{"score", "qty"}, Label: "scale",
				Params: json.RawMessage(`{"scale_min":0,"scale_max":20}`)},
			{Type: types.ATTR_PROFILE_SIMILARITY, Vector: "v", Label: "keyed",
				Params: json.RawMessage(`{"metric":"cosine","reference":{"table":"targets","key_field":"region"}}`)},
			{Type: types.ATTR_PROFILE_SIMILARITY, Vector: "v", Label: "jac",
				Params: json.RawMessage(`{"metric":"jaccard","reference":{"s":1,"q":0}}`)},
			{Type: attrTallyMerge, Field: "score", Label: "ext"},
			// ATTR_VEC_DISTANCE over row-local metrics (E3-S2
			// follow-up): the keyed one misses west, hellinger's
			// negative reference part is a domain miss on every row.
			{Type: types.ATTR_VEC_DISTANCE, Vector: "v", Label: "dkeyed",
				Params: json.RawMessage(`{"metric":"manhattan","reference":{"table":"targets","key_field":"region"}}`)},
			{Type: types.ATTR_VEC_DISTANCE, Vector: "v", Label: "ddom",
				Params: json.RawMessage(`{"metric":"hellinger","reference":{"s":-1,"q":1}}`)},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "scale", Label: "sum_scale"},
			{Type: types.AGG_SUM, Field: "keyed", Label: "sum_keyed"},
			{Type: types.AGG_COUNT, Field: "jac", Label: "n_jac"},
			{Type: types.AGG_SUM, Field: "ext", Label: "sum_ext"},
			{Type: types.AGG_SUM, Field: "dkeyed", Label: "sum_dkeyed"},
			{Type: types.AGG_COUNT, Field: "ddom", Label: "n_ddom"},
		},
	}
	if groups {
		req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
	}
	return req
}

// tallyArm is one parallel arm: the partitioned instance and the
// sequential reference over the same rows.
type tallyArm struct {
	name     string
	parallel observe.Arm
	open     func(t *testing.T, rec *execRecorder) (p *pulse.Pulse, path string, ref *pulse.Pulse, refPath string)
}

func tallyArms(t *testing.T) []tallyArm {
	large := parityLargeCohort(t.TempDir())
	return []tallyArm{
		{"shard_archive", observe.ArmShardParallel, func(t *testing.T, rec *execRecorder) (*pulse.Pulse, string, *pulse.Pulse, string) {
			fsys := afero.NewMemMapFs()
			s := paritySchema(t)
			shards := []string{"s0.pulse", "s1.pulse", "s2.pulse"}
			for i, path := range shards {
				writeParityCohort(t, fsys, path, s, i*40, (i+1)*40)
			}
			writeParityCohort(t, fsys, "whole.pulse", s, 0, 120)
			p := execNew(t, pulse.Options{FS: fsys, ShardWorkers: 3, Extensions: tallyExtensions()}, rec)
			if _, err := p.CreateShardArchive(context.Background(), "archive.pulse", shards); err != nil {
				t.Fatalf("CreateShardArchive: %v", err)
			}
			ref, err := pulse.New(pulse.Options{FS: fsys, ShardWorkers: 1, DecodeWorkers: 1, Extensions: tallyExtensions()})
			if err != nil {
				t.Fatal(err)
			}
			return p, "archive.pulse", ref, "whole.pulse"
		}},
		{"parallel_decode", observe.ArmParallelDecode, func(t *testing.T, rec *execRecorder) (*pulse.Pulse, string, *pulse.Pulse, string) {
			dir := large(t)
			ref, err := pulse.New(pulse.Options{DataDir: dir, ShardWorkers: 1, DecodeWorkers: 1, Extensions: tallyExtensions()})
			if err != nil {
				t.Fatal(err)
			}
			return execNew(t, pulse.Options{DataDir: dir, DecodeWorkers: 4, Extensions: tallyExtensions()}, rec), "large.pulse", ref, "large.pulse"
		}},
	}
}

// lastArmOf is the arm of the last recorded operation.
func lastArmOf(t *testing.T, rec *execRecorder) observe.Arm {
	t.Helper()
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.ends) == 0 {
		t.Fatal("no operation recorded")
	}
	return rec.ends[len(rec.ends)-1].Arm
}

func warnString(ws []*types.ResponseWarning) string {
	b, _ := json.Marshal(ws)
	return string(b)
}

// TestTallyMerge_ChainStages: the shared gate admits the tally
// attributes to ProcessChain stages — at runtime and in the chain
// validator alike — and keeps the centroid form and an undeclared
// component-emitting extension refused on both sides.
func TestTallyMerge_ChainStages(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	writeParityCohort(t, fsys, "c.pulse", paritySchema(t), 0, paritySmallRows)
	p, err := pulse.New(pulse.Options{FS: fsys, Extensions: tallyExtensions()})
	if err != nil {
		t.Fatal(err)
	}
	chain := func(attrs []*types.Attribute, field string) *types.ChainRequest {
		return &types.ChainRequest{
			Cohort: &types.Cohort{Filename: "c.pulse"},
			Stages: []*types.ChainStage{
				{Name: "s0", Request: &types.Request{
					Vectors:      []types.VectorSpec{{Name: "v", Fields: []string{"score", "qty"}, Labels: []string{"s", "q"}}},
					Attributes:   attrs,
					Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: field, Label: "total"}},
					Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				}},
				{Name: "s1", Request: &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "total", Label: "grand"}}}},
			},
		}
	}
	full := tallyRequest("c.pulse", false).Attributes
	cases := []struct {
		name    string
		attrs   []*types.Attribute
		field   string
		refused bool
	}{
		{"scale score", full[:1], "scale", false},
		{"keyed profile similarity", full[1:2], "keyed", false},
		{"mergeable extension", full[3:4], "ext", false},
		{"row-local vec distance", full[4:5], "dkeyed", false},
		{"fitted vec distance", []*types.Attribute{{Type: types.ATTR_VEC_DISTANCE, Vector: "v", Label: "f",
			Params: json.RawMessage(`{"metric":"mahalanobis","reference":{"s":1,"q":1}}`)}}, "f", true},
		{"centroid vec distance", []*types.Attribute{{Type: types.ATTR_VEC_DISTANCE, Vector: "v", Label: "c",
			Params: json.RawMessage(`{"metric":"euclidean","reference":{"centroid":{}}}`)}}, "c", true},
		{"centroid profile similarity", []*types.Attribute{{Type: types.ATTR_PROFILE_SIMILARITY, Vector: "v", Label: "c",
			Params: json.RawMessage(`{"metric":"cosine","reference":{"centroid":{}}}`)}}, "c", true},
		{"undeclared extension", []*types.Attribute{{Type: attrTallySolo, Field: "score", Label: "ext"}}, "ext", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, rerr := p.ProcessChain(ctx, chain(tc.attrs, tc.field))
			env, err := p.PredictChain(ctx, chain(tc.attrs, tc.field))
			if err != nil {
				t.Fatal(err)
			}
			if !tc.refused {
				if rerr != nil {
					t.Fatalf("runtime refused: %v", rerr)
				}
				if len(env.Errors) != 0 {
					t.Fatalf("validator refused what runtime accepts: %+v", env.Errors)
				}
				return
			}
			assertCodedError(t, rerr, perr.PULSE_CHAIN_NOT_MERGEABLE)
			if len(env.Errors) == 0 || env.Errors[0].Code != string(perr.PULSE_CHAIN_NOT_MERGEABLE) {
				t.Errorf("validator admits what runtime refuses: %+v", env.Errors)
			}
		})
	}
}

// TestTallyMerge_PredictFanOutMatchesRuntime: predict's shard fan-out
// verdict (descx.ShardFanOut, which charges the per-shard partials)
// reads the same gate over the declared extension facts — it predicts
// the fan-out for the tally request (the declared extension included)
// and a serial run for the undeclared extension, as the runtime routes.
func TestTallyMerge_PredictFanOutMatchesRuntime(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rec := &execRecorder{}
	p, path, _, _ := tallyArms(t)[0].open(t, rec)
	cohort, err := p.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	in := descx.LimitInputs{ShardWorkers: 3, ShardRecords: []int64{40, 40, 40}, Extensions: facadebridge.ExtensionsSnapshot(p)}
	solo := tallyRequest(path, false)
	solo.Attributes = []*types.Attribute{{Type: attrTallySolo, Field: "score", Label: "ext"}}
	solo.Aggregations = []*types.Aggregation{{Type: types.AGG_SUM, Field: "ext", Label: "sum_ext"}}
	fitted := tallyRequest(path, false)
	fitted.Attributes = []*types.Attribute{{Type: types.ATTR_VEC_DISTANCE, Vector: "v", Label: "f",
		Params: json.RawMessage(`{"metric":"std_euclidean","reference":{"s":1,"q":1}}`)}}
	fitted.Aggregations = []*types.Aggregation{{Type: types.AGG_SUM, Field: "f", Label: "sum_f"}}
	for name, tc := range map[string]struct {
		req  *types.Request
		fans bool
	}{"tally": {tallyRequest(path, false), true}, "undeclared extension": {solo, false}, "fitted vec distance": {fitted, false}} {
		t.Run(name, func(t *testing.T) {
			if _, err := p.Process(ctx, tc.req); err != nil {
				t.Fatalf("Process: %v", err)
			}
			runtimeFans := lastArmOf(t, rec) == observe.ArmShardParallel
			predicted := descx.ShardFanOut(tc.req, cohort.Schema(), facadebridge.InstanceSnapshot(p), in)
			if predicted != runtimeFans || runtimeFans != tc.fans {
				t.Errorf("predict fan-out %v, runtime %v, want %v", predicted, runtimeFans, tc.fans)
			}
		})
	}
}

// TestTallyMerge_ExtensionMergeableRefusals: a Mergeable attribute
// registration is probe-validated at pulse.New — Mode must be row_local,
// an emitting value must implement extend.MergeableAttribute, and the
// ComponentSchema must not be "none".
func TestTallyMerge_ExtensionMergeableRefusals(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		edit   func(*pulse.AttributeRegistration)
		reason string // "" = accepted
	}{
		{"declared and implemented", func(*pulse.AttributeRegistration) {}, ""},
		{"no components needs no method", func(r *pulse.AttributeRegistration) {
			r.ComponentSchema, r.ComponentsFunc = descriptor.ComponentSchema{}, nil
			r.Factory = func(*types.Attribute, *encoding.Schema) (extend.AttributeComputer, error) { return &highAttr{}, nil }
		}, ""},
		{"buffered mode", func(r *pulse.AttributeRegistration) { r.Mode = pulse.AttributeModeBuffered }, "mergeable_without_row_local"},
		{"missing merge interface", func(r *pulse.AttributeRegistration) {
			r.Factory = func(*types.Attribute, *encoding.Schema) (extend.AttributeComputer, error) { return &highAttr{}, nil }
			r.ComponentsFunc = func(inst extend.AttributeComputer) (map[string]any, error) {
				return map[string]any{"n_high": inst.(*highAttr).high}, nil
			}
		}, "missing_merge_interface"},
		{"none mergeability", func(r *pulse.AttributeRegistration) {
			s := highSchema()
			s.Mergeability = descriptor.None
			r.ComponentSchema = s
		}, "components_not_mergeable"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reg := tallyRegistration(attrTallyMerge, true)
			c.edit(&reg)
			_, err := pulse.New(newTestOptions(pulse.Extensions{Attributes: []pulse.AttributeRegistration{reg}}))
			if c.reason == "" {
				if err != nil {
					t.Fatalf("pulse.New: %v", err)
				}
				return
			}
			assertCodedError(t, err, perr.PULSE_EXTENSION_MERGEABLE_MISMATCH)
			var ce *perr.CodedError
			_ = stderrors.As(err, &ce)
			if ce.Details["reason"] != c.reason || ce.Details["category"] != "attribute" {
				t.Errorf("details %v, want reason %q category attribute", ce.Details, c.reason)
			}
		})
	}
}

// TestTallyMerge_ManifestDeclaresMergeable: the declaration reaches the
// extension snapshot the manifest and predict read.
func TestTallyMerge_ManifestDeclaresMergeable(t *testing.T) {
	t.Parallel()

	p, err := pulse.New(newTestOptions(tallyExtensions()))
	if err != nil {
		t.Fatal(err)
	}
	m := p.Manifest(context.Background())
	got := map[string]bool{}
	for _, a := range m.Extensions.Attributes {
		got[a.Name] = a.Mergeable
	}
	if !got[string(attrTallyMerge)] || got[string(attrTallySolo)] {
		t.Errorf("manifest extension attribute mergeable = %v, want %s only", got, attrTallyMerge)
	}
}
