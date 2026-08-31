package processing

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// This file is the buffered-path twin of
// crosstab_fused_margin_agg_test.go. It shares that file's fixture
// (auxMarginSchema / auxMarginRecords / auxMarginSpec) deliberately: the
// whole point of the story is that ONE request produces the SAME
// auxiliary figures on both dispatch arms, and two fixtures could not
// prove that.
//
// Like the fused tests, these read the accumulated figures directly off
// the path's own carrier rather than off a Response. Nothing is on the
// wire on either arm yet — E2-S5 widens populateCrosstabComponents — so
// there is no response field to assert against, and reading the carrier
// is the only way to see the numbers at all.

// driveBufferedAuxMargin runs the buffered auxiliary-margin computation
// over the shared fixture and hands back the carrier RunCrosstab builds.
// It mirrors driveAuxMargin (the fused twin) step for step: same schema,
// same records, same spec, no filters.
func driveBufferedAuxMargin(t *testing.T, spec *types.CrosstabSpec) *crosstabAuxMargins {
	t.Helper()
	aux, _, _ := driveBufferedAuxMarginParts(t, spec)
	return aux
}

// driveBufferedAuxMarginParts additionally returns the two axis
// partitions, so a test can address a slot by the same key the carrier
// is keyed on.
func driveBufferedAuxMarginParts(t *testing.T, spec *types.CrosstabSpec) (*crosstabAuxMargins, *CrosstabAxisPartition, *CrosstabAxisPartition) {
	t.Helper()
	schema := auxMarginSchema(t)
	records := auxMarginRecords(schema)
	p := NewProcessor(schema)
	rowPart, err := p.PartitionByAxis(spec.Rows, records)
	if err != nil {
		t.Fatalf("PartitionByAxis(rows): %v", err)
	}
	colPart, err := p.PartitionByAxis(spec.Columns, records)
	if err != nil {
		t.Fatalf("PartitionByAxis(columns): %v", err)
	}
	aux, err := p.computeAuxMargins(spec, records, rowPart, colPart)
	if err != nil {
		t.Fatalf("computeAuxMargins: %v", err)
	}
	return aux, rowPart, colPart
}

// bufferedAuxFinal is the buffered counterpart of auxFinal: it reads one
// auxiliary figure out of a slot and returns its scalar value plus the
// universal-floor counters, with an absent figure reading as zero
// exactly as an unconstructed fused accumulator does.
func bufferedAuxFinal(t *testing.T, slot []auxMarginFigure, idx int) (float64, int, int) {
	t.Helper()
	if idx >= len(slot) {
		t.Fatalf("auxiliary index %d out of range (%d figures)", idx, len(slot))
	}
	fig := slot[idx]
	if !fig.Present {
		return 0, fig.N, fig.NNull
	}
	v, ok := fig.Value.(float64)
	if !ok {
		t.Fatalf("auxiliary figure %d is %T, want float64", idx, fig.Value)
	}
	return v, fig.N, fig.NNull
}

// TestCrosstab_BufferedAuxMarginObservesCellAdmission is the buffered
// twin of TestFusedCrosstab_AuxMarginObservesCellAdmission, and it must
// stay a SEPARATE assertion from the parity test below rather than being
// folded into it: a parity test alone passes just as happily when BOTH
// paths get the admission rule wrong in the same direction, which is
// exactly what a shared helper or a copied condition would produce.
//
// Getting this wrong is silent — every number still renders, the base is
// merely cohort-wide instead of metric-scoped — so each of the three
// exclusions carries a diagnostic naming which admission was lost.
func TestCrosstab_BufferedAuxMarginObservesCellAdmission(t *testing.T) {
	aux, _, colPart := driveBufferedAuxMarginParts(t, auxMarginSpec())

	// Grand slot: respondents 1 and 5 are the only admitted records.
	base, n, nNull := bufferedAuxFinal(t, aux.Grand, 0)
	if base != 2 {
		t.Errorf("grand auxiliary distinct_count = %v, want 2 (respondents 1 and 5); "+
			"3 means the null cell field was admitted, 4 means the Include-excluded row was", base)
	}
	if n != 2 || nNull != 0 {
		t.Errorf("grand auxiliary floor = {n:%d, n_null:%d}, want {2, 0}", n, nNull)
	}

	weighted, _, _ := bufferedAuxFinal(t, aux.Grand, 1)
	if weighted != 17 {
		t.Errorf("grand auxiliary distinct_sum = %v, want 17 (weights 1 + 16); "+
			"21 admits the null-metric row, 19 admits the Include-excluded row, 25 admits the null column key",
			weighted)
	}

	// One row key survives the Include: alpha. Both admitted records sit
	// in it, so the row figure equals the grand figure here.
	rowBase, _, _ := bufferedAuxFinal(t, aux.Rows["alpha"], 0)
	if rowBase != 2 {
		t.Errorf("alpha row auxiliary distinct_count = %v, want 2", rowBase)
	}

	// Column `f` holds respondents 1 (admitted), 2 (Include-excluded on
	// the ROW axis) and 3 (null cell field); only respondent 1 is
	// admitted. This is the slot that proves a column auxiliary is
	// narrowed by the OTHER axis's resolution.
	colBase, _, _ := bufferedAuxFinal(t, aux.Cols["f"], 0)
	if colBase != 1 {
		t.Errorf("f column auxiliary distinct_count = %v, want 1 (respondent 1 only); "+
			"2 means the Include-excluded row key was admitted, 3 means the null cell field was too", colBase)
	}
	if got := len(colPart.Records["f"]); got != 3 {
		t.Fatalf("fixture drift: column f routes %d records, want 3", got)
	}
}

// TestCrosstab_BufferedAuxMarginMatchesFused is the acceptance criterion
// of this story stated directly: ONE request, both dispatch arms, the
// auxiliary figures identical.
//
// It is not decoration. Dispatch picks fused or buffered on request
// SHAPE, and nothing in Response reports which ran, so a divergence here
// makes a sample-size figure move for reasons that have nothing to do
// with sample size and with no way for a caller to tell.
//
// The fan-out case is the one E2-S2's handoff called out specifically:
// deriving admission inside the cell double-loop would update a ROW
// auxiliary once per (row, column) pair — M times under a column
// fan-out — where the fused walk updates it once per distinct row key.
// That divergence is invisible on a one-key-per-record axis, so a
// parity test without a fan-out arm would pass against the wrong
// implementation.
func TestCrosstab_BufferedAuxMarginMatchesFused(t *testing.T) {
	specs := map[string]func() *types.CrosstabSpec{
		"all margins": auxMarginSpec,
		"rows only": func() *types.CrosstabSpec {
			s := auxMarginSpec()
			s.Margins = types.CrosstabMargins{Rows: true}
			return s
		},
		"columns only": func() *types.CrosstabSpec {
			s := auxMarginSpec()
			s.Margins = types.CrosstabMargins{Columns: true}
			return s
		},
		"grand only": func() *types.CrosstabSpec {
			s := auxMarginSpec()
			s.Margins = types.CrosstabMargins{Grand: true}
			return s
		},
		"no include": func() *types.CrosstabSpec {
			s := auxMarginSpec()
			s.Rows = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "brand"}}
			return s
		},
		// Include beta only. Column `m` then routes respondent 5 and
		// admits nobody, so this arm is the one that compares an
		// OCCUPIED-BUT-EMPTY slot: the fused path leaves its accumulator
		// unconstructed and the buffered path must leave its figure
		// absent, rather than running an aggregator over zero records and
		// reporting whatever it happens to return.
		// normalize implies a margin even with margins.* false, so an
		// auxiliary rides the implied slot on both arms. The PARTIAL-depth
		// and cross-axis margins normalize additionally builds get no
		// auxiliary on either arm — they exist solely as denominators for
		// the cell, and an auxiliary is never a denominator — so this arm
		// is what would catch one path growing an auxiliary there.
		"normalize row implies the margin": func() *types.CrosstabSpec {
			s := auxMarginSpec()
			s.Rows = []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "brand"},
				{Type: types.GROUP_CATEGORY, Field: "gender"},
			}
			s.Margins = types.CrosstabMargins{}
			s.Normalize = types.CrosstabNormalizeRow
			level := 0
			s.NormalizeLevel = &level
			return s
		},
		"include beta only": func() *types.CrosstabSpec {
			s := auxMarginSpec()
			s.Rows = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "brand", Include: []string{"beta"}}}
			return s
		},
	}
	for name, mk := range specs {
		t.Run(name, func(t *testing.T) {
			assertAuxMarginParity(t, mk())
		})
	}

	t.Run("column fan-out", func(t *testing.T) {
		assertFanoutAuxMarginParity(t)
	})
}

// assertAuxMarginParity drives one spec down both arms over the shared
// fixture and compares every occupied slot.
func assertAuxMarginParity(t *testing.T, spec *types.CrosstabSpec) {
	t.Helper()
	fused := driveAuxMargin(t, spec)
	buffered, _, _ := driveBufferedAuxMarginParts(t, spec)

	if buffered == nil {
		t.Fatalf("buffered arm produced no auxiliary carrier for a spec declaring %d auxiliaries",
			len(spec.MarginAggregations))
	}

	cmp := func(what string, fusedSlot []auxMarginAccumulator, bufSlot []auxMarginFigure) {
		t.Helper()
		if len(fusedSlot) != len(bufSlot) {
			t.Errorf("%s: fused holds %d auxiliaries, buffered %d", what, len(fusedSlot), len(bufSlot))
			return
		}
		for i := range fusedSlot {
			fv, fn, fnn := auxFinal(t, fusedSlot, i)
			bv, bn, bnn := bufferedAuxFinal(t, bufSlot, i)
			if fv != bv || fn != bn || fnn != bnn {
				t.Errorf("%s auxiliary %d: fused {value:%v n:%d n_null:%d}, buffered {value:%v n:%d n_null:%d}",
					what, i, fv, fn, fnn, bv, bn, bnn)
			}
			// PRESENCE, not just the number. A slot that admitted no
			// record must carry no figure on either arm — the fused path
			// never constructs its accumulator, and the buffered path
			// must likewise not run an aggregator over zero records and
			// report whatever it returns. The two are numerically
			// indistinguishable for the distinct operators (both read 0),
			// which is exactly why the difference has to be asserted
			// directly: E2-S5 emits these per margin slot, and an honest
			// blank and a fabricated base of 0 are not the same claim.
			if fp, bp := fusedSlot[i].agg != nil, bufSlot[i].Present; fp != bp {
				t.Errorf("%s auxiliary %d: fused figure present=%v, buffered present=%v",
					what, i, fp, bp)
			}
		}
	}

	if spec.NeedsGrandMargin() {
		cmp("grand", fused.grandMarginAux, buffered.Grand)
	}
	if spec.NeedsRowMargin() {
		if len(fused.rowKeys) != len(buffered.Rows) {
			t.Errorf("row slots: fused %d keys, buffered %d", len(fused.rowKeys), len(buffered.Rows))
		}
		for idx, rkey := range fused.rowKeys {
			cmp("row["+rkey+"]", fused.rowMarginAux[idx], buffered.Rows[rkey])
		}
	}
	if spec.NeedsColumnMargin() {
		if len(fused.colKeys) != len(buffered.Cols) {
			t.Errorf("column slots: fused %d keys, buffered %d", len(fused.colKeys), len(buffered.Cols))
		}
		for idx, ckey := range fused.colKeys {
			cmp("column["+ckey+"]", fused.colMarginAux[idx], buffered.Cols[ckey])
		}
	}
}

// assertFanoutAuxMarginParity reuses the fan-out fixture
// (crosstab_fused_fanout_test.go) so a record lands in SEVERAL column
// keys at once. A row auxiliary must still see that record once.
func assertFanoutAuxMarginParity(t *testing.T) {
	t.Helper()
	schema := fanoutCrosstabSchema(t)
	records := fanoutRecords(schema)
	spec := &types.CrosstabSpec{
		Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
		Columns: []*types.Group{{Type: types.GROUP_SET_PER_ELEMENT, Field: "chans"}},
		Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value"},
		Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		MarginAggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "value", Label: "aux_total"},
			{Type: types.AGG_DISTINCT_COUNT, Field: "region", Label: "regions"},
		},
	}

	fusedState, err := NewFusedCrosstabState(spec, schema, &ExtensionRegistry{})
	if err != nil {
		t.Fatalf("NewFusedCrosstabState: %v", err)
	}
	for _, r := range records {
		fusedState.AddTotalRow()
		if err := fusedState.Update(r); err != nil {
			t.Fatalf("fused Update: %v", err)
		}
	}

	p := NewProcessor(schema)
	rowPart, err := p.PartitionByAxis(spec.Rows, records)
	if err != nil {
		t.Fatalf("PartitionByAxis(rows): %v", err)
	}
	colPart, err := p.PartitionByAxis(spec.Columns, records)
	if err != nil {
		t.Fatalf("PartitionByAxis(columns): %v", err)
	}
	buffered, err := p.computeAuxMargins(spec, records, rowPart, colPart)
	if err != nil {
		t.Fatalf("computeAuxMargins: %v", err)
	}

	// Non-vacuity: the fan must actually fire, or this arm is a
	// second copy of the one-key-per-record case above.
	if len(colPart.Keys) < 2 {
		t.Fatalf("fan-out fixture produced %d column keys; the fan never fired", len(colPart.Keys))
	}

	for idx, rkey := range fusedState.rowKeys {
		fv, fn, fnn := auxFinal(t, fusedState.rowMarginAux[idx], 0)
		bv, bn, bnn := bufferedAuxFinal(t, buffered.Rows[rkey], 0)
		if fv != bv || fn != bn || fnn != bnn {
			t.Errorf("row[%s] aux_total under a column fan-out: fused {value:%v n:%d n_null:%d}, "+
				"buffered {value:%v n:%d n_null:%d}; a buffered figure that is a MULTIPLE of the fused "+
				"one means admission was derived inside the cell double-loop, which counts a record "+
				"once per (row, column) pair", rkey, fv, fn, fnn, bv, bn, bnn)
		}
	}
	for idx, ckey := range fusedState.colKeys {
		fv, fn, fnn := auxFinal(t, fusedState.colMarginAux[idx], 0)
		bv, bn, bnn := bufferedAuxFinal(t, buffered.Cols[ckey], 0)
		if fv != bv || fn != bn || fnn != bnn {
			t.Errorf("column[%s] aux_total: fused {value:%v n:%d n_null:%d}, buffered {value:%v n:%d n_null:%d}",
				ckey, fv, fn, fnn, bv, bn, bnn)
		}
	}
	fv, fn, fnn := auxFinal(t, fusedState.grandMarginAux, 0)
	bv, bn, bnn := bufferedAuxFinal(t, buffered.Grand, 0)
	if fv != bv || fn != bn || fnn != bnn {
		t.Errorf("grand aux_total: fused {value:%v n:%d n_null:%d}, buffered {value:%v n:%d n_null:%d}",
			fv, fn, fnn, bv, bn, bnn)
	}
}

// TestCrosstab_BufferedAuxMarginNoAllocationWhenAbsent mirrors the fused
// allocation test from both directions: nothing at all when the slot is
// undeclared, and only the requested margin slots when it is.
func TestCrosstab_BufferedAuxMarginNoAllocationWhenAbsent(t *testing.T) {
	t.Run("slot absent", func(t *testing.T) {
		spec := auxMarginSpec()
		spec.MarginAggregations = nil
		if aux := driveBufferedAuxMargin(t, spec); aux != nil {
			t.Errorf("auxiliary carrier allocated with no margin_aggregations declared: %+v", aux)
		}
	})

	t.Run("column margin only", func(t *testing.T) {
		spec := auxMarginSpec()
		spec.Margins = types.CrosstabMargins{Columns: true}
		aux := driveBufferedAuxMargin(t, spec)
		if aux == nil || aux.Cols == nil {
			t.Fatalf("column auxiliary figures not built for a column-margin spec")
		}
		if aux.Rows != nil {
			t.Errorf("row auxiliary figures built for a spec with no row margin")
		}
		if aux.Grand != nil {
			t.Errorf("grand auxiliary figure built for a spec with no grand margin")
		}
	})

	t.Run("no margin observed", func(t *testing.T) {
		spec := auxMarginSpec()
		spec.Margins = types.CrosstabMargins{}
		aux := driveBufferedAuxMargin(t, spec)
		if aux != nil && (aux.Rows != nil || aux.Cols != nil || aux.Grand != nil) {
			t.Errorf("auxiliary figures built for a spec that emits no margin at all: %+v", aux)
		}
	})
}

// TestCrosstab_BufferedAuxMarginNonMergeableCell covers the realistic
// buffered caller. AGG_WELFORD and AGG_MEDIAN are non-mergeable, so they
// force the buffered path by construction — and two BERA stat-test
// families are built on AGG_WELFORD, so a Visa metric added on either
// reaches this code and nothing else.
//
// The gate assertion is the non-vacuity control: without it the case
// would silently become a third fused test the day the gate widened.
func TestCrosstab_BufferedAuxMarginNonMergeableCell(t *testing.T) {
	for _, cellType := range []types.AggregationType{types.AGG_WELFORD, types.AGG_MEDIAN} {
		t.Run(string(cellType), func(t *testing.T) {
			spec := auxMarginSpec()
			spec.Cell = &types.Aggregation{Type: cellType, Field: "metric"}

			schema := auxMarginSchema(t)
			if ok, _ := CanFuseCrosstab(&types.Request{Crosstab: spec}, schema, nil); ok {
				t.Fatalf("%s cell admitted to the fused path; this case no longer "+
					"exercises the buffered arm it was written for", cellType)
			}

			aux, _, _ := driveBufferedAuxMarginParts(t, spec)
			if aux == nil {
				t.Fatalf("no auxiliary figures computed for a %s cell", cellType)
			}
			// The admission rule reads the CELL FIELD's nullity, not the
			// cell aggregator's type, so the figures are the same ones
			// the AGG_SUM spec produces.
			base, n, nNull := bufferedAuxFinal(t, aux.Grand, 0)
			if base != 2 || n != 2 || nNull != 0 {
				t.Errorf("grand auxiliary under a %s cell = {value:%v n:%d n_null:%d}, want {2, 2, 0}",
					cellType, base, n, nNull)
			}
			weighted, _, _ := bufferedAuxFinal(t, aux.Grand, 1)
			if weighted != 17 {
				t.Errorf("grand auxiliary distinct_sum under a %s cell = %v, want 17", cellType, weighted)
			}
		})
	}
}

// TestCrosstab_BufferedAuxMarginUnresolvableTypeRefused proves the
// RunCrosstab call site is LIVE rather than a held local nothing reaches.
//
// An auxiliary naming an aggregator no registry knows passes
// validateCrosstabSpec (MarginReducibility falls through to a classified
// default) and declines fusion (an unknown type is not Mergeable), so
// before this story the buffered path accepted it and silently produced
// no figure for it. Both arms must now refuse it, and the buffered
// refusal can only come from the accumulation call.
//
// The three arms are NOT redundant. Only the first is caught by the
// aggregation itself; the other two reach no aggregation at all, and are
// what the up-front registry resolution exists for — it mirrors
// NewFusedCrosstabState, which resolves every auxiliary at construction
// whether or not a slot will ever admit a record. Without them, deleting
// that resolution is a mutation nothing detects, and the two arms would
// disagree about a request neither can serve.
func TestCrosstab_BufferedAuxMarginUnresolvableTypeRefused(t *testing.T) {
	bogus := func() []*types.Aggregation {
		return []*types.Aggregation{
			{Type: types.AggregationType("AGG_NOT_A_REAL_OPERATOR"), Field: "weight", Label: "bogus"},
		}
	}

	cases := map[string]func() *types.CrosstabSpec{
		"slots admit records": func() *types.CrosstabSpec {
			s := auxMarginSpec()
			s.MarginAggregations = bogus()
			return s
		},
		// Every row key excluded, so no slot admits a single record and
		// no aggregator is ever constructed by the accumulation itself.
		"no slot admits a record": func() *types.CrosstabSpec {
			s := auxMarginSpec()
			s.Rows = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "brand", Include: []string{"nonesuch"}}}
			s.MarginAggregations = bogus()
			return s
		},
		// No margin emitted at all: legal, predict-warned, and the
		// auxiliary has nowhere to land — but naming an operator that
		// does not exist is still a refusal, not a shrug.
		"no margin observed": func() *types.CrosstabSpec {
			s := auxMarginSpec()
			s.Margins = types.CrosstabMargins{}
			s.MarginAggregations = bogus()
			return s
		},
	}

	schema := auxMarginSchema(t)
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			spec := mk()
			// Non-vacuity: this request genuinely routes to the buffered path.
			if ok, _ := CanFuseCrosstab(&types.Request{Crosstab: spec}, schema, nil); ok {
				t.Fatalf("unknown auxiliary type admitted to the fused path")
			}

			p := NewProcessor(schema)
			_, err := p.RunCrosstab(t.Context(), &types.Request{Crosstab: spec}, auxMarginRecords(schema))
			if err == nil {
				t.Fatalf("RunCrosstab accepted an unresolvable margin aggregation; the fused arm " +
					"refuses it at construction, so the two arms disagree about the same request")
			}
			if !errors.HasCode(err, errors.PROCESSING_CONFIG) {
				t.Errorf("expected %s, got %v", errors.PROCESSING_CONFIG, err)
			}
		})
	}
}

// TestCrosstab_BufferedAuxMarginLabelsTrackDeclarationOrder pins the one
// piece of the carrier E2-S5 keys its wire payload on. Margin components
// are keyed by effective label, so a carrier whose Labels drifted out of
// step with its figure slices would key every auxiliary figure under the
// wrong name — a silent mislabelling, not a missing field.
func TestCrosstab_BufferedAuxMarginLabelsTrackDeclarationOrder(t *testing.T) {
	spec := auxMarginSpec()
	aux := driveBufferedAuxMargin(t, spec)
	want := spec.MarginAggregationLabels()
	if len(aux.Labels) != len(want) {
		t.Fatalf("carrier holds %d labels, spec declares %d", len(aux.Labels), len(want))
	}
	for i := range want {
		if aux.Labels[i] != want[i] {
			t.Errorf("label %d = %q, want %q", i, aux.Labels[i], want[i])
		}
	}
	if len(aux.Grand) != len(want) {
		t.Errorf("grand slot holds %d figures for %d labels", len(aux.Grand), len(want))
	}
}
