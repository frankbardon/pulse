package processing

import (
	stderrors "errors"
	"math"
	"slices"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/linalg/metric"
	"github.com/frankbardon/pulse/types"
)

// matrix_distance.go is MAT_DISTANCE: the k × k distance matrix between
// the centroids of the buckets a grouper (params.between) cuts the rows
// into. The slot keeps one BlockCoMoments per bucket beside its base
// state (every bucketed row, once), so each centroid is the bucket's
// weighted mean off the one fixed merge tree, and the floor counts and
// the pooled covariance come from the same states.
//
// A streaming between grouper (types.GroupType.Streamable) keys each row
// as it arrives (GroupKeyer), so the slot streams and — when the
// grouper's keys merge — merges bucket by bucket. Any other grouper
// (GROUP_KMEANS, GROUP_QUANTILE) needs the whole row set: the slot keeps
// the records and folds them at finalize, after one Grouper.Group pass,
// in record order (a buffered spec never merges).

// distanceState is a MAT_DISTANCE slot's between-grouper state.
type distanceState struct {
	grouper Grouper
	field   string
	// keyer is the streaming key dispatch; nil on a buffered between.
	keyer *GroupKeyer
	// records are a buffered between's rows, folded at prepare.
	records []*Record
	// buckets holds one co-moment set per bucket key.
	buckets map[string]*BlockCoMoments
	// nNullKey counts the rows the grouper placed in no bucket (a null
	// key, an include rejection, a GROUP_KMEANS row with a null
	// member). Summed at Merge.
	nNullKey int64
	prepared bool
	// warnings are the grouper's own (GROUP_KMEANS non-convergence).
	warnings []*types.ResponseWarning
}

// newDistanceState builds the between grouper of plan for a slot whose
// stamped weight is weight. A GROUP_KMEANS between reads the slot's
// weight (w coincident points); no other grouper's keys read one.
func newDistanceState(plan vectors.Matrix, weight *types.WeightSpec, schema *encoding.Schema, exts *ExtensionRegistry) (*distanceState, error) {
	if plan.Between == nil {
		return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			"MAT_DISTANCE plan carries no between grouper", map[string]any{"matrix": plan.Name})
	}
	grp := *plan.Between
	grp.Weight = types.SlotWeight{}
	if grp.Type == types.GROUP_KMEANS && weight != nil {
		grp.Weight = types.SlotWeightOf(*weight)
	}
	factory, ok := exts.LookupGrouper(grp.Type)
	if !ok {
		return nil, errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION,
			"matrix "+plan.Name+": params.between references unknown grouper type "+string(grp.Type),
			map[string]any{"matrix": plan.Name, "reason": "unknown_between_type", "type": string(grp.Type)})
	}
	g, err := factory(&grp, schema)
	if err != nil {
		return nil, err
	}
	ApplyGrouperExtensions(g, exts)
	st := &distanceState{grouper: g, field: grp.Field, buckets: map[string]*BlockCoMoments{}}
	if plan.Streamable {
		if st.keyer, err = NewGroupKeyer(g); err != nil {
			return nil, err
		}
	}
	return st, nil
}

// update keys one filter-passing record: a streaming between folds it
// into its buckets now, a buffered one keeps it for prepare.
func (d *distanceState) update(m *matrixSlot, r *Record) error {
	if d.keyer == nil {
		d.records = append(d.records, r)
		return nil
	}
	keys, ok, err := d.keyer.Keys(r, d.field)
	if err != nil {
		return err
	}
	if !ok {
		d.nNullKey++
		return nil
	}
	return d.fold(m, r, keys)
}

// fold reads r's members and weight once and adds the row to the base
// state and to every bucket keys names.
func (d *distanceState) fold(m *matrixSlot, r *Record, keys []string) error {
	w, err := m.readRow(r)
	if err != nil {
		return err
	}
	if err := m.state.Add(r, m.x, w); err != nil {
		return err
	}
	for _, k := range keys {
		b, ok := d.buckets[k]
		if !ok {
			if b, err = NewBlockCoMoments(len(m.cols), linalg.Listwise); err != nil {
				return err
			}
			d.buckets[k] = b
		}
		if err := b.Add(r, m.x, w); err != nil {
			return err
		}
	}
	return nil
}

// prepare runs a buffered between's whole-set pass once: the grouper
// partitions the kept records, then every record is folded in record
// order into the buckets it landed in. A streaming between has nothing
// to do.
func (d *distanceState) prepare(m *matrixSlot) error {
	if d.prepared {
		return nil
	}
	d.prepared = true
	if d.keyer == nil {
		groups, err := d.grouper.Group(d.records, d.field)
		if err != nil {
			return err
		}
		keysOf := make(map[*Record][]string, len(d.records))
		for k, recs := range groups {
			for _, r := range recs {
				keysOf[r] = append(keysOf[r], k)
			}
		}
		for _, r := range d.records {
			keys := keysOf[r]
			if len(keys) == 0 {
				d.nNullKey++
				continue
			}
			if err := d.fold(m, r, keys); err != nil {
				return err
			}
		}
		d.records = nil
	}
	d.warnings = GrouperWarnings(d.grouper)
	return nil
}

// order returns the bucket keys in the grouper's emission order (its
// intrinsic order — GROUP_KMEANS by size — or its include list, else
// alphabetical): the order the grouped Data rows of the same grouper
// take.
func (d *distanceState) order() []string {
	keys := make([]string, 0, len(d.buckets))
	for k := range d.buckets {
		keys = append(keys, k)
	}
	return orderKeysByInclude(includeFilterOf(d.grouper), keys)
}

// merge absorbs o's buckets (a key one side alone saw moves over whole)
// and null-key count. A buffered between never merges.
func (d *distanceState) merge(name string, o *distanceState) error {
	if d.keyer == nil || o == nil || o.keyer == nil {
		return errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			"matrix merge: a buffered MAT_DISTANCE slot is not mergeable",
			map[string]any{"matrix": name, "type": string(types.MAT_DISTANCE)})
	}
	for k, b := range o.buckets {
		if mine, ok := d.buckets[k]; ok {
			if err := mine.Absorb(b); err != nil {
				return err
			}
			continue
		}
		d.buckets[k] = b
	}
	o.buckets = map[string]*BlockCoMoments{}
	d.nNullKey += o.nNullKey
	return nil
}

// distanceBucket is one bucket's merged state at finalize.
type distanceBucket struct {
	key      string
	cm       *linalg.CoMoment
	centroid []float64
}

// finalizeDistance is MAT_DISTANCE's finalizer: the centroids, the
// metric over them (standardized when asked), the parts the plan
// renders and the warnings.
func finalizeDistance(in *matrixFinalizeInput) (matrixOutput, error) {
	m := in.slot
	d := m.dist
	plan := in.Plan()
	cm := in.CM
	keys := d.order()
	k := len(keys)
	buckets := make([]distanceBucket, k)
	for i, key := range keys {
		bcm, err := d.buckets[key].Tree()
		if err != nil {
			return matrixOutput{}, err
		}
		buckets[i] = distanceBucket{key: key, cm: bcm, centroid: bcm.Mean().Slice()}
	}

	// The weighted-inference rule: every figure is the frequency formula
	// on w* = w·N*/Σw, N* = Σw (unweighted, frequency) or Kish n_eff
	// (probability). scale = N*/Σw.
	nStar, scale := cm.W(), 1.0
	if m.weight != nil && m.weight.EffectiveKind() == types.WeightKindProbability {
		nStar = cm.NEff()
		scale = nStar / cm.W()
	}

	var warns []*types.ResponseWarning
	if cm.N() < 2 || !(cm.W() > 0) {
		warns = append(warns, matrixWarning(errors.PULSE_MATRIX_INSUFFICIENT_N,
			"matrix "+plan.Name+" rests on fewer than 2 bucketed rows or no weight mass; its distances are undefined or degenerate",
			map[string]any{"matrix": plan.Name, "scope": "matrix", "n": cm.N(), "sum_weights": cm.W()}))
	}
	var thin []string
	for _, b := range buckets {
		if !(b.cm.W() > 0) {
			thin = append(thin, b.key)
		}
	}
	if len(thin) > 0 {
		warns = append(warns, matrixWarning(errors.PULSE_MATRIX_INSUFFICIENT_N,
			"matrix "+plan.Name+" has buckets with no complete, weighted row; their centroids and distances are null",
			map[string]any{"matrix": plan.Name, "scope": "buckets", "buckets": thin}))
	}

	// The points the metric compares: the centroids, or under
	// standardize their z-scores over the bucketed rows (R scale(): the
	// mean and the N* − 1 spread).
	points := make([][]float64, k)
	for i := range buckets {
		points[i] = buckets[i].centroid
	}
	var std [][]float64
	if plan.Standardize {
		mean := cm.Mean().Slice()
		sd := memberSD(cm, nStar, m.weight)
		var flat []string
		for j := range sd {
			if sd[j] == 0 {
				flat = append(flat, m.cols[j])
			}
		}
		if len(flat) > 0 && cm.N() >= 2 && cm.W() > 0 {
			warns = append(warns, matrixWarning(errors.PULSE_MATRIX_ZERO_VARIANCE,
				"matrix "+plan.Name+" has members with zero spread; they have no z-score, so the standardized distances skip them (the element sums rescaled by p/p_observed)",
				map[string]any{"matrix": plan.Name, "members": flat}))
		}
		std = make([][]float64, k)
		for i := range buckets {
			std[i] = standardizeCentroid(buckets[i].centroid, mean, sd)
		}
		points = std
	}
	if share := plan.MaxDropShare; share != nil {
		rows := cm.N() + cm.NWeightInvalid() + m.dropped
		if rows > 0 {
			if got := float64(m.dropped) / float64(rows); got > *share {
				warns = append(warns, matrixWarning(errors.PULSE_MATRIX_LISTWISE_HEAVY_DROP,
					"listwise deletion dropped more rows from matrix "+plan.Name+" than params.max_drop_share allows",
					map[string]any{"matrix": plan.Name, "dropped": m.dropped, "rows": rows, "share": got, "max_drop_share": *share}))
			}
		}
	}

	// The metric resolves through the linalg/metric registry (decode
	// already refused anything the MAT_DISTANCE consumer does not
	// accept). mahalanobis reads the pooled within-bucket covariance's
	// inverse over the RAW centroids; every other metric the points.
	met, err := metric.ResolveFor(metric.ConsumerMatDistance, plan.Metric, metric.Distance)
	if err != nil {
		return matrixOutput{}, err
	}
	var pooled *linalg.Sym
	var stats *metric.Stats
	if met.Fit() == metric.FitInverseCovariance {
		var inv *linalg.Sym
		var w *types.ResponseWarning
		pooled, inv, w = pooledInverse(plan.Name, m.cols, buckets, scale, nStar)
		if w != nil {
			warns = append(warns, w)
		}
		if inv != nil {
			stats = &metric.Stats{InverseCovariance: inv}
		}
		points = make([][]float64, k)
		for i := range buckets {
			points[i] = buckets[i].centroid
		}
	}
	dom := &distanceDomain{}
	dist := func(a, b int) float64 {
		return centroidDistance(met, stats, plan.MinValid, points[a], points[b], buckets[a].key, buckets[b].key, dom)
	}
	warns = append(warns, d.warnings...)

	cells := make([][]float64, k)
	for a := range cells {
		cells[a] = make([]float64, k)
	}
	for a := 0; a < k; a++ {
		for b := a; b < k; b++ {
			v := dist(a, b)
			if a == b && !math.IsNaN(v) {
				v = 0
			}
			cells[a][b], cells[b][a] = v, v
		}
	}
	if w := dom.warning(plan.Name, plan.Metric); w != nil {
		warns = append(warns, w)
	}
	out := matrixOutput{Primary: bucketMatrix(keys, plan.Encoding, cells), Warnings: warns}
	if in.WantVectors() {
		sizes := make([]float64, k)
		counts := make([]int, k)
		for i, b := range buckets {
			sizes[i], counts[i] = b.cm.W(), int(b.cm.N())
		}
		out.Vectors = map[string]any{"sizes": sizes, "counts": counts}
	}
	if in.WantAuxiliary() {
		members, _ := plan.OutputMembers()
		out.Auxiliary = map[string]*types.MatrixValues{"centroids": centroidMatrix(keys, members, buckets, nil)}
		if std != nil {
			out.Auxiliary["centroids_standardized"] = centroidMatrix(keys, members, nil, std)
		}
		if pooled != nil {
			out.Auxiliary["pooled_covariance"] = in.Square(pooled.At)
		}
	}
	return out, nil
}

// standardizeCentroid is c z-scored by mean and sd, member by member.
// A member with no positive, finite spread has no z-score: it is a
// MISSING coordinate (NaN), dropped pairwise by the metric — never the
// ±Inf a rounding residual over a zero sd would give.
func standardizeCentroid(c, mean, sd []float64) []float64 {
	z := make([]float64, len(c))
	for j := range z {
		if !(sd[j] > 0) || math.IsInf(sd[j], 0) {
			z[j] = math.NaN()
			continue
		}
		z[j] = (c[j] - mean[j]) / sd[j]
	}
	return z
}

// memberSD is each member's standard deviation over the bucketed rows:
// √(M2/(N* − 1)) on w* — Cov(1) unweighted and under frequency weights,
// Cov(0)·n_eff/(n_eff − 1) under probability weights. NaN where N* ≤ 1.
func memberSD(cm *linalg.CoMoment, nStar float64, weight *types.WeightSpec) []float64 {
	p := cm.P()
	out := make([]float64, p)
	if weight != nil && weight.EffectiveKind() == types.WeightKindProbability {
		c0 := cm.Cov(0)
		for j := range out {
			v := math.NaN()
			if nStar > 1 {
				v = c0.At(j, j) * (nStar / (nStar - 1))
			}
			out[j] = math.Sqrt(v)
		}
		return out
	}
	c1 := cm.Cov(1)
	for j := range out {
		out[j] = math.Sqrt(c1.At(j, j))
	}
	return out
}

// centroidDistance is met between centroids a and b (bucket keys ka,
// kb) under the registry's pairwise-complete policy: a coordinate
// undefined on either side (a massless bucket's centroid, a zero-spread
// member's z-score) is dropped and the Scaled metrics' element sum is
// rescaled by p/p_observed; fewer than minValid complete pairs, or none,
// is null. mahalanobis (Complete) is null on any missing coordinate and
// when the pooled inverse is undefined (stats nil). A value outside the
// metric's domain (a negative coordinate under hellinger / aitchison, an
// aitchison zero part) is null and recorded in dom for the
// PULSE_MATRIX_METRIC_DOMAIN warning. ±Inf is an ordinary value.
func centroidDistance(met metric.Metric, stats *metric.Stats, minValid int, a, b []float64, ka, kb string, dom *distanceDomain) float64 {
	if obs, _ := metric.Observed(a, b); obs == 0 || obs < minValid {
		return math.NaN()
	}
	if met.Fitted() && stats == nil {
		return math.NaN()
	}
	v, err := met.Eval(metric.Distance, a, b, stats)
	if err != nil {
		var de *metric.DomainError
		if stderrors.As(err, &de) {
			dom.add(ka, kb, de.Reason)
		}
		return math.NaN()
	}
	return v
}

// distanceSortedKeys returns m's keys in ascending order.
func distanceSortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// distanceDomain collects the centroid pairs a metric's domain refused.
type distanceDomain struct {
	buckets map[string]bool
	reasons map[string]bool
	pairs   int
}

func (d *distanceDomain) add(ka, kb, reason string) {
	if d.buckets == nil {
		d.buckets, d.reasons = map[string]bool{}, map[string]bool{}
	}
	d.buckets[ka], d.buckets[kb] = true, true
	d.reasons[reason] = true
	d.pairs++
}

// warning is PULSE_MATRIX_METRIC_DOMAIN when any pair was refused: the
// buckets involved and the reasons, both sorted.
func (d *distanceDomain) warning(name, metricName string) *types.ResponseWarning {
	if d.pairs == 0 {
		return nil
	}
	return matrixWarning(errors.PULSE_MATRIX_METRIC_DOMAIN,
		"matrix "+name+": some centroids hold values outside the "+metricName+" metric's domain (a negative coordinate, or an aitchison zero part); the distances touching them are null",
		map[string]any{"matrix": name, "metric": metricName, "pairs": d.pairs, "buckets": distanceSortedKeys(d.buckets), "reasons": distanceSortedKeys(d.reasons)})
}

// pooledInverse is the pooled within-bucket covariance on w* —
// S = (N*/Σw)·Σ_b M2_b / (N* − k), k the buckets with mass, summed in
// emission order — and its inverse through the reference Cholesky. An
// undefined pool (N* ≤ k) or a singular S (the one rank rule,
// rankDeficient) nulls every distance with a warning
// (PULSE_MATRIX_INSUFFICIENT_N scope "pooled", or PULSE_MATRIX_SINGULAR
// naming the members).
func pooledInverse(name string, members []string, buckets []distanceBucket, scale, nStar float64) (*linalg.Sym, *linalg.Sym, *types.ResponseWarning) {
	p := len(members)
	sum, _ := linalg.NewSym(p, nil)
	massed := 0
	for _, b := range buckets {
		w := b.cm.W()
		if !(w > 0) {
			continue
		}
		massed++
		c0 := b.cm.Cov(0)
		for i := 0; i < p; i++ {
			for j := i; j < p; j++ {
				sum.Set(i, j, sum.At(i, j)+float64(c0.At(i, j)*w))
			}
		}
	}
	df := nStar - float64(massed)
	pooled, _ := linalg.NewSym(p, nil)
	for i := 0; i < p; i++ {
		for j := i; j < p; j++ {
			v := math.NaN()
			if df > 0 {
				v = float64(scale*sum.At(i, j)) / df
			}
			pooled.Set(i, j, v)
		}
	}
	if !(df > 0) {
		return pooled, nil, matrixWarning(errors.PULSE_MATRIX_INSUFFICIENT_N,
			"matrix "+name+" has no more rows than buckets, so the pooled within-bucket covariance mahalanobis reads is undefined; its distances are null",
			map[string]any{"matrix": name, "scope": "pooled", "n_star": nStar, "buckets": massed})
	}
	singular := func() *types.ResponseWarning {
		return matrixWarning(errors.PULSE_MATRIX_SINGULAR,
			"matrix "+name+": the pooled within-bucket covariance is singular (a member is constant within buckets or a linear combination of others), so its mahalanobis distances are null",
			map[string]any{"matrix": name, "outputs": []string{"primary"}, "reason": "pooled_covariance", "members": append([]string(nil), members...)})
	}
	// The DECISION is the one rank rule (rankDeficient, shared with
	// ATTR_MAHALANOBIS and the multivariate tests), so a dependency
	// rounding leaves a tiny positive pivot on is still singular; the
	// VALUE is the reference (FMA-free) inverse.
	if rankDeficient(pooled.ToRows()) {
		return pooled, nil, singular()
	}
	inv, err := linalg.InverseSPD(pooled)
	if err != nil {
		return pooled, nil, singular()
	}
	return pooled, inv, nil
}

// bucketMatrix renders the k × k distances keyed by the bucket labels
// in enc (full rows, or the upper triangle).
func bucketMatrix(keys []string, enc types.MatrixEncoding, cells [][]float64) *types.MatrixValues {
	k := len(keys)
	out := &types.MatrixValues{
		Kind:       types.MatrixKindSquareSymmetric,
		Encoding:   enc,
		RowKeys:    append([]string(nil), keys...),
		ColumnKeys: append([]string(nil), keys...),
		Values:     make([][]float64, k),
	}
	for r := 0; r < k; r++ {
		start := 0
		if enc == types.MatrixEncodingUpper {
			start = r
		}
		out.Values[r] = append([]float64(nil), cells[r][start:]...)
	}
	return out
}

// centroidMatrix renders the k × p centroids (rectangular: a row per
// bucket, a column per member) from the buckets' means, or from rows
// when given (the standardized centroids).
func centroidMatrix(keys, members []string, buckets []distanceBucket, rows [][]float64) *types.MatrixValues {
	out := &types.MatrixValues{
		Kind:       types.MatrixKindRectangular,
		Encoding:   types.MatrixEncodingFull,
		RowKeys:    append([]string(nil), keys...),
		ColumnKeys: append([]string(nil), members...),
		Values:     make([][]float64, len(keys)),
	}
	for i := range keys {
		if rows != nil {
			out.Values[i] = append([]float64(nil), rows[i]...)
		} else {
			out.Values[i] = append([]float64(nil), buckets[i].centroid...)
		}
	}
	return out
}

// distanceComponents adds MAT_DISTANCE's operator keys to its
// Response.Components.Matrices entry.
func (m *matrixSlot) distanceComponents(c *types.MatrixComponents) {
	if c.Operator == nil {
		c.Operator = map[string]any{}
	}
	c.Operator["metric"] = m.plan.Metric
	c.Operator["standardize"] = m.plan.Standardize
	c.Operator["buckets"] = len(m.dist.buckets)
	c.Operator["n_null_key"] = int(m.dist.nNullKey)
}
