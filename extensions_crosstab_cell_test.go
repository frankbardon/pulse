package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Extension aggregator as a crosstab CELL (U02b E4-E). A registration
// declaring a MarginReducibility class other than recompute (and
// Mergeable, which implies Streamable) takes the fused crosstab arm
// exactly like its built-in twin; an undeclared one keeps the buffered
// arm. The fused walk computes every margin from independent row /
// column / grand accumulators fed record by record, so the declared
// class gates admission only — it never changes the margin arithmetic.

const (
	aggXtSum     types.AggregationType = "AGG_XT_SUM"
	aggXtMean    types.AggregationType = "AGG_XT_MEAN"
	aggXtUndecl  types.AggregationType = "AGG_XT_UNDECLARED"
	aggXtRecomp  types.AggregationType = "AGG_XT_RECOMPUTE"
	aggXtNoMerge types.AggregationType = "AGG_XT_NOMERGE"
)

// parityMean is an extend reimplementation of AGG_AVERAGE: nulls are
// skipped, an empty input is 0, Components carries {sum}.
type parityMean struct {
	probe   *parityProbe
	sum     float64
	n       int64
	touched bool
}

var _ extend.MergeableAggregator = (*parityMean)(nil)

func (a *parityMean) Aggregate(rows extend.Rows, field string) (float64, error) {
	a.probe.buffered.Add(1)
	for i := 0; i < rows.Len(); i++ {
		if v, ok := rows.At(i).NumericValue(field); ok {
			a.sum += v
			a.n++
		}
	}
	return a.mean(), nil
}

func (a *parityMean) UpdateRow(r extend.Record, field string) error {
	if !a.touched {
		a.touched = true
		a.probe.online.Add(1)
	}
	if v, ok := r.NumericValue(field); ok {
		a.sum += v
		a.n++
	}
	return nil
}

func (a *parityMean) mean() float64 {
	if a.n == 0 {
		return 0
	}
	return a.sum / float64(a.n)
}

func (a *parityMean) Finalize() (float64, error) { return a.mean(), nil }

func (a *parityMean) Merge(other extend.OnlineAggregator) error {
	o, ok := other.(*parityMean)
	if !ok {
		return fmt.Errorf("parityMean.Merge: got %T", other)
	}
	a.probe.merges.Add(1)
	a.sum += o.sum
	a.n += o.n
	return nil
}

func parityMeanRegistration(probe *parityProbe, name types.AggregationType) pulse.AggregatorRegistration {
	return pulse.AggregatorRegistration{
		Name:        name,
		Description: "Test-only extend reimplementation of AGG_AVERAGE.",
		Factory: func(*types.Aggregation, *encoding.Schema) (extend.Aggregator, error) {
			return &parityMean{probe: probe}, nil
		},
		Streamable:         true,
		Mergeable:          true,
		MarginReducibility: types.MarginMeanReducible,
		Accepts:            []encoding.FieldType{encoding.FieldTypeF64, encoding.FieldTypeU32},
		FieldInputs:        func(json.RawMessage) []string { return nil },
		ComponentSchema: descriptor.ComponentSchema{
			Keys:         []descriptor.ComponentKey{{Name: "sum", Type: "float64", Description: "Running sum."}},
			Mergeability: descriptor.Mergeable,
		},
		ComponentsFunc: func(inst extend.Aggregator) (map[string]any, error) {
			return map[string]any{"sum": inst.(*parityMean).sum}, nil
		},
	}
}

// crosstabCellExtensions registers the summable sum twin, the
// mean-reducible mean twin, and three sum twins that must NOT fuse:
// one declaring no class, one declaring recompute, one declaring a
// class while being Mergeable=false is refused by the probe and is
// therefore not registered here.
func crosstabCellExtensions(probe *parityProbe) pulse.Extensions {
	sum := paritySumRegistration(probe, aggXtSum, true)
	sum.MarginReducibility = types.MarginSummable
	recomp := paritySumRegistration(probe, aggXtRecomp, true)
	recomp.MarginReducibility = types.MarginRecompute
	return pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{
		sum,
		parityMeanRegistration(probe, aggXtMean),
		paritySumRegistration(probe, aggXtUndecl, true),
		recomp,
	}}
}

// crosstabCellCase is one {built-in, extension} crosstab pair that
// differs only in the cell (and auxiliary) aggregation type.
type crosstabCellCase struct {
	name    string
	builtin types.AggregationType
	ext     types.AggregationType
	field   string
	// aux: carry an auxiliary margin aggregation of the same pair.
	aux bool
	// normalize: the crosstab normalize mode.
	normalize types.CrosstabNormalize
	// builtinFuses: whether the built-in arm itself takes the fused
	// arm (false for a decimal128 cell, which built-ins keep buffered).
	builtinFuses bool
	// mustContain: substrings the built-in Crosstab JSON must carry.
	mustContain []string
}

func (c crosstabCellCase) request(at, aux types.AggregationType) *types.Request {
	spec := &types.CrosstabSpec{
		Rows:      []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
		Columns:   []*types.Group{{Type: types.GROUP_RANGE, Field: "qty", Interval: 5}},
		Cell:      &types.Aggregation{Type: at, Field: c.field, Label: "total"},
		Margins:   types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		Normalize: c.normalize,
	}
	if c.aux {
		spec.MarginAggregations = []*types.Aggregation{{Type: aux, Field: "score", Label: "aux"}}
	}
	return &types.Request{Cohort: &types.Cohort{Filename: "parity.pulse"}, Crosstab: spec}
}

func crosstabCellCases() []crosstabCellCase {
	return []crosstabCellCase{
		{name: "summable_sum", builtin: types.AGG_SUM, ext: aggXtSum, field: "qty", aux: true,
			builtinFuses: true, mustContain: []string{`"present":true`, `"north"`, `"aux"`}},
		{name: "summable_sum_nullable", builtin: types.AGG_SUM, ext: aggXtSum, field: "score",
			builtinFuses: true, mustContain: []string{`"present":true`, `"n_null"`}},
		{name: "summable_sum_normalize_row", builtin: types.AGG_SUM, ext: aggXtSum, field: "qty",
			normalize: types.CrosstabNormalizeRow, builtinFuses: true, mustContain: []string{`"present":true`}},
		{name: "mean_reducible_mean", builtin: types.AGG_AVERAGE, ext: aggXtMean, field: "score",
			builtinFuses: true, mustContain: []string{`"present":true`, `"sum"`}},
		// decimal128: the built-in AGG_SUM cell stays buffered (the wide
		// decimal fold is buffered-only); the extension twin reads
		// DecimalValue per row and fuses on its declaration.
		{name: "summable_sum_decimal", builtin: types.AGG_SUM, ext: aggXtSum, field: "amount",
			builtinFuses: false, mustContain: []string{`"present":true`, `"value":"`}},
	}
}

// renameXt rewrites extension operator names in the extension arm's
// JSON to their built-in twins before comparison.
func renameXt(s string, c crosstabCellCase) string {
	return strings.ReplaceAll(s, `"`+string(c.ext)+`"`, `"`+string(c.builtin)+`"`)
}

// TestExtensions_DeclaredMarginCellFusesCrosstab pins that an extension
// cell aggregator declaring a non-recompute MarginReducibility takes
// the fused crosstab arm (gate admits it; the engine drives it only
// through UpdateRow, never the buffered Aggregate) and that the matrix,
// margins, auxiliary margin figures, Components and Metadata equal the
// built-in twin's in every execution mode.
func TestExtensions_DeclaredMarginCellFusesCrosstab(t *testing.T) {
	large := parityLargeCohort(t.TempDir())
	for _, mode := range parityModes(large) {
		if mode.decorate != nil {
			continue
		}
		t.Run(mode.name, func(t *testing.T) {
			probe := &parityProbe{}
			p, path := mode.open(t, crosstabCellExtensions(probe))
			ctx := context.Background()
			schema := paritySchema(t)
			reg := pulse.ServiceForTest(p).Extensions()
			for _, c := range crosstabCellCases() {
				t.Run(c.name, func(t *testing.T) {
					bReq, eReq := c.request(c.builtin, types.AGG_SUM), c.request(c.ext, aggXtSum)
					bReq.Cohort.Filename, eReq.Cohort.Filename = path, path
					if ok, why := processing.CanFuseCrosstab(bReq, schema, reg); ok != c.builtinFuses {
						t.Fatalf("CanFuseCrosstab(built-in %s) = %v (%s), want %v", c.builtin, ok, why, c.builtinFuses)
					}
					if ok, why := processing.CanFuseCrosstab(eReq, schema, reg); !ok {
						t.Errorf("CanFuseCrosstab(extension %s) = false (%s), want true", c.ext, why)
					}
					builtin, err := p.Process(ctx, bReq)
					if err != nil {
						t.Fatalf("built-in crosstab: %v", err)
					}
					probe.reset()
					ext, err := p.Process(ctx, eReq)
					if err != nil {
						t.Fatalf("extension crosstab: %v", err)
					}
					if probe.online.Load() == 0 || probe.buffered.Load() != 0 || probe.merges.Load() != 0 {
						t.Errorf("extension cell path: online=%d buffered=%d merges=%d, want the fused per-record arm only",
							probe.online.Load(), probe.buffered.Load(), probe.merges.Load())
					}
					if c.field == "amount" && probe.decimalReads.Load() == 0 {
						t.Error("decimal cell: extension never reached DecimalValue")
					}
					b, e := mustJSON(t, builtin.Crosstab), renameXt(mustJSON(t, ext.Crosstab), c)
					if b != e {
						t.Errorf("Crosstab differs\nbuiltin:   %s\nextension: %s", b, e)
					}
					for _, sub := range c.mustContain {
						if !strings.Contains(b+mustJSON(t, builtin.Components), sub) {
							t.Fatalf("built-in crosstab lacks %s; parity over nothing proves nothing: %s", sub, b)
						}
					}
					bc, ec := mustJSON(t, builtin.Components), renameXt(mustJSON(t, ext.Components), c)
					if bc != ec {
						t.Errorf("Components differ\nbuiltin:   %s\nextension: %s", bc, ec)
					}
					if bm, em := mustJSON(t, builtin.Metadata), mustJSON(t, ext.Metadata); bm != em {
						t.Errorf("Metadata differs\nbuiltin:   %s\nextension: %s", bm, em)
					}
				})
			}
		})
	}
}

// TestExtensions_UndeclaredMarginCellStaysBuffered pins that an
// extension cell aggregator declaring no MarginReducibility — or
// recompute — keeps today's behaviour: the gate declines it, the
// engine drives it through the buffered Aggregate, and the result
// still equals the built-in's (which itself fused).
func TestExtensions_UndeclaredMarginCellStaysBuffered(t *testing.T) {
	probe := &parityProbe{}
	fsys := afero.NewMemMapFs()
	writeParityCohort(t, fsys, "parity.pulse", paritySchema(t), 0, paritySmallRows)
	p, err := pulse.New(pulse.Options{FS: fsys, Extensions: crosstabCellExtensions(probe)})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	ctx := context.Background()
	schema := paritySchema(t)
	reg := pulse.ServiceForTest(p).Extensions()
	for _, at := range []types.AggregationType{aggXtUndecl, aggXtRecomp} {
		t.Run(string(at), func(t *testing.T) {
			c := crosstabCellCase{builtin: types.AGG_SUM, ext: at, field: "qty", aux: true}
			bReq, eReq := c.request(types.AGG_SUM, types.AGG_SUM), c.request(at, types.AGG_SUM)
			ok, why := processing.CanFuseCrosstab(eReq, schema, reg)
			if ok || !strings.Contains(why, "recompute-margin cell aggregator") {
				t.Fatalf("CanFuseCrosstab(%s) = %v (%q), want false (recompute-margin)", at, ok, why)
			}
			builtin, err := p.Process(ctx, bReq)
			if err != nil {
				t.Fatalf("built-in crosstab: %v", err)
			}
			probe.reset()
			ext, err := p.Process(ctx, eReq)
			if err != nil {
				t.Fatalf("extension crosstab: %v", err)
			}
			if probe.buffered.Load() == 0 || probe.online.Load() != 0 {
				t.Errorf("undeclared cell path: online=%d buffered=%d, want buffered only",
					probe.online.Load(), probe.buffered.Load())
			}
			if b, e := mustJSON(t, builtin.Crosstab), renameXt(mustJSON(t, ext.Crosstab), c); b != e {
				t.Errorf("Crosstab differs\nbuiltin:   %s\nextension: %s", b, e)
			}
		})
	}
}

// TestExtensions_ExtensionAuxMarginAggregationFuses pins the auxiliary
// half of the gate: an extension margin_aggregations entry is admitted
// on its Mergeable declaration (no class needed — an auxiliary is
// independent in role), and a non-Mergeable one declines fusion rather
// than being dropped.
func TestExtensions_ExtensionAuxMarginAggregationFuses(t *testing.T) {
	probe := &parityProbe{}
	ext := crosstabCellExtensions(probe)
	ext.Aggregators = append(ext.Aggregators, paritySumRegistration(probe, aggXtNoMerge, false))
	fsys := afero.NewMemMapFs()
	writeParityCohort(t, fsys, "parity.pulse", paritySchema(t), 0, paritySmallRows)
	p, err := pulse.New(pulse.Options{FS: fsys, Extensions: ext})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	schema := paritySchema(t)
	reg := pulse.ServiceForTest(p).Extensions()
	c := crosstabCellCase{field: "qty", aux: true}
	// The undeclared-class twin is Mergeable: fine as an auxiliary.
	if ok, why := processing.CanFuseCrosstab(c.request(types.AGG_SUM, aggXtUndecl), schema, reg); !ok {
		t.Errorf("Mergeable extension auxiliary declined fusion: %s", why)
	}
	// Decimal-typed auxiliary field on an extension: admitted (the
	// extension reads DecimalValue), unlike a built-in auxiliary.
	dec := c.request(types.AGG_SUM, aggXtUndecl)
	dec.Crosstab.MarginAggregations[0].Field = "amount"
	if ok, why := processing.CanFuseCrosstab(dec, schema, reg); !ok {
		t.Errorf("decimal extension auxiliary declined fusion: %s", why)
	}
	dec.Crosstab.MarginAggregations[0].Type = types.AGG_SUM
	if ok, _ := processing.CanFuseCrosstab(dec, schema, reg); ok {
		t.Error("decimal built-in auxiliary admitted to fusion")
	}
	if ok, why := processing.CanFuseCrosstab(c.request(types.AGG_SUM, aggXtNoMerge), schema, reg); ok ||
		!strings.Contains(why, "non-mergeable margin aggregation") {
		t.Errorf("non-Mergeable extension auxiliary: CanFuseCrosstab = %v (%q), want false", ok, why)
	}
}

func marginMismatch(t *testing.T, reg pulse.AggregatorRegistration, reason string) {
	t.Helper()
	_, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(),
		Extensions: pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{reg}}})
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("expected *errors.CodedError, got %T: %v", err, err)
	}
	if ce.Code != perr.PULSE_EXTENSION_MARGIN_REDUCIBILITY_MISMATCH {
		t.Fatalf("code = %s (%s), want PULSE_EXTENSION_MARGIN_REDUCIBILITY_MISMATCH", ce.Code, ce.Message)
	}
	if got := ce.Details["reason"]; got != reason {
		t.Errorf("details.reason = %v, want %s", got, reason)
	}
	if got := ce.Details["name"]; got != string(reg.Name) {
		t.Errorf("details.name = %v, want %s", got, reg.Name)
	}
}

// TestExtensions_ProbeAggregator_MarginReducibilityMismatch covers the
// probe triggers for a declared MarginReducibility.
func TestExtensions_ProbeAggregator_MarginReducibilityMismatch(t *testing.T) {
	probe := &parityProbe{}
	t.Run("unknown_class", func(t *testing.T) {
		reg := paritySumRegistration(probe, aggXtSum, true)
		reg.MarginReducibility = "sumable"
		marginMismatch(t, reg, "unknown_class")
	})
	t.Run("not_mergeable", func(t *testing.T) {
		reg := paritySumRegistration(probe, aggXtSum, false)
		reg.MarginReducibility = types.MarginSummable
		marginMismatch(t, reg, "margin_without_mergeable")
	})
	t.Run("not_streamable", func(t *testing.T) {
		reg := paritySumRegistration(probe, aggXtSum, false)
		reg.Streamable = false
		reg.MarginReducibility = types.MarginIndependent
		marginMismatch(t, reg, "margin_without_mergeable")
	})
	for _, mr := range []types.MarginReducibility{types.MarginSummable, types.MarginMeanReducible, types.MarginIndependent} {
		t.Run("accepted_"+string(mr), func(t *testing.T) {
			reg := paritySumRegistration(probe, aggXtSum, true)
			reg.MarginReducibility = mr
			if _, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(),
				Extensions: pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{reg}}}); err != nil {
				t.Fatalf("declared %s refused: %v", mr, err)
			}
		})
	}
	t.Run("recompute_needs_nothing", func(t *testing.T) {
		// recompute is the not-fusable class: an explicit spelling of
		// the omitted default, so it demands no Mergeable.
		reg := paritySumRegistration(probe, aggXtSum, false)
		reg.Streamable = false
		reg.MarginReducibility = types.MarginRecompute
		if _, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(),
			Extensions: pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{reg}}}); err != nil {
			t.Fatalf("declared recompute refused: %v", err)
		}
	})
}

// TestExtensions_ManifestProjectsMarginReducibility pins the snapshot
// projection: the declared class reaches the manifest's extensions
// block; an undeclared registration omits it.
func TestExtensions_ManifestProjectsMarginReducibility(t *testing.T) {
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: crosstabCellExtensions(&parityProbe{})})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	got := map[string]string{}
	for _, m := range p.Manifest(context.Background()).Extensions.Aggregators {
		got[m.Name] = m.MarginReducibility
	}
	want := map[string]string{
		string(aggXtSum):    string(types.MarginSummable),
		string(aggXtMean):   string(types.MarginMeanReducible),
		string(aggXtUndecl): "",
		string(aggXtRecomp): string(types.MarginRecompute),
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("manifest margin_reducibility[%s] = %q, want %q", k, got[k], v)
		}
	}
}

// TestExtensions_PredictCrosstabNormalizeReadsDeclaredMargin pins the
// predict twin: the PULSE_CROSSTAB_NORMALIZE_UNSATISFIABLE advisory —
// raised for a recompute-margin cell under normalize — reads an
// extension cell's DECLARED class through the snapshot, so a declared
// summable cell is not flagged while an undeclared one still is.
func TestExtensions_PredictCrosstabNormalizeReadsDeclaredMargin(t *testing.T) {
	var buf []byte
	w := &byteSink{b: &buf}
	if err := encoding.WriteHeader(w); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(w, paritySchema(t)); err != nil {
		t.Fatal(err)
	}
	buf = append(buf, parityPayload(t, paritySchema(t), 0, 8)...)
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: crosstabCellExtensions(&parityProbe{})})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	flagged := func(at types.AggregationType) bool {
		c := crosstabCellCase{field: "qty", normalize: types.CrosstabNormalizeRow}
		env, err := p.PredictBytes(context.Background(), buf, c.request(at, types.AGG_SUM))
		if err != nil {
			t.Fatalf("PredictBytes(%s): %v", at, err)
		}
		for _, e := range env.Warnings {
			if e.Code == string(perr.PULSE_CROSSTAB_NORMALIZE_UNSATISFIABLE) {
				return true
			}
		}
		return false
	}
	for at, want := range map[types.AggregationType]bool{
		types.AGG_SUM: false, aggXtSum: false, aggXtMean: false,
		types.AGG_MEDIAN: true, aggXtUndecl: true, aggXtRecomp: true,
	} {
		if got := flagged(at); got != want {
			t.Errorf("predict normalize advisory for %s = %v, want %v", at, got, want)
		}
	}
}
