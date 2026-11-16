package feature

import (
	"fmt"
	"math"
	"math/rand"
	"sort"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/internal/splitmode"
	"github.com/frankbardon/pulse/types"
)

func init() {
	register(types.FEAT_TRAIN_TEST_SPLIT, newTrainTestSplit)
}

// SplitTrain, SplitVal, SplitTest are the encoded values for the output
// column. The skill documents the mapping; downstream filters and
// aggregations reference these constants by their numeric form.
const (
	SplitTrain float64 = 0
	SplitVal   float64 = 1
	SplitTest  float64 = 2
)

// trainTestSplitParams configures the split. Ratios sum to 1.0 (within
// 1e-6 tolerance). When Stratify names a categorical field, the operator
// applies the ratios independently within each category so class balance
// is preserved across train/val/test partitions.
//
//	{"ratios": [0.7, 0.15, 0.15], "seed": 42, "stratify": "label"}
//
// Two-element ratios produce only train/val (no test); three-element
// ratios produce all three splits.
//
// Mode "temporal" (internal/splitmode) cuts at a time boundary instead:
// the earliest rows by OrderBy go to train, the latest to test, val
// between, and rows sharing an OrderBy value never straddle a boundary.
// A null OrderBy cannot be placed in time, so its row is in no partition
// (a null split value). Temporal mode runs buffered (featstream).
//
//	{"ratios": [0.8, 0.2], "mode": "temporal", "order_by": "week"}
type trainTestSplitParams struct {
	Ratios []float64 `json:"ratios"`
	// Seed is a pointer only so temporal mode can refuse a seed the
	// request names; random mode reads an absent seed as 0, as before.
	Seed     *int64 `json:"seed"`
	Stratify string `json:"stratify,omitempty"`
	Mode     string `json:"mode,omitempty"`
	OrderBy  string `json:"order_by,omitempty"`
}

type trainTestSplit struct {
	label    string
	ratios   []float64
	seed     int64
	stratify string
	// orderBy is set in temporal mode only (internal/splitmode).
	orderBy string
	// Streaming state. PrePass records either the row count alone (no
	// stratify) or the per-row stratify key in iteration order. Finalize
	// materializes the assignment table so EmitRow can index by emitIdx.
	rowCount     int
	stratifyKeys []string
	assignments  []float64
	emitIdx      int
}

func newTrainTestSplit(feat *types.Feature, schema *encoding.Schema) (Computer, error) {
	if len(feat.Params) == 0 {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"FEAT_TRAIN_TEST_SPLIT requires params with 'ratios'")
	}
	var p trainTestSplitParams
	if err := paramread.Decode(paramread.Feature, string(feat.Type), feat.Params, &p); err != nil {
		return nil, errors.WrapCodedError(err, errors.PROCESSING_CONFIG,
			"parsing FEAT_TRAIN_TEST_SPLIT params")
	}
	if len(p.Ratios) < 2 || len(p.Ratios) > 3 {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			"FEAT_TRAIN_TEST_SPLIT: 'ratios' must have 2 or 3 elements")
	}
	var sum float64
	for _, r := range p.Ratios {
		if r < 0 {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				"FEAT_TRAIN_TEST_SPLIT: all ratios must be non-negative")
		}
		sum += r
	}
	if math.Abs(sum-1.0) > 1e-6 {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			fmt.Sprintf("FEAT_TRAIN_TEST_SPLIT: ratios sum %f != 1.0", sum))
	}

	if cerr := splitmode.Check(splitmode.Params{Mode: p.Mode, OrderBy: p.OrderBy, SeedSet: p.Seed != nil, Stratify: p.Stratify}); cerr != nil {
		return nil, cerr
	}
	if cerr := fieldaccept.Check(string(types.FEAT_TRAIN_TEST_SPLIT), fieldaccept.SlotStratify, p.Stratify, schema); cerr != nil {
		return nil, cerr
	}
	if cerr := fieldaccept.Check(string(types.FEAT_TRAIN_TEST_SPLIT), fieldaccept.SlotOrderBy, p.OrderBy, schema); cerr != nil {
		return nil, cerr
	}
	var seed int64
	if p.Seed != nil {
		seed = *p.Seed
	}

	label := feat.Label
	if label == "" {
		label = "split"
	}
	return &trainTestSplit{
		label:    label,
		ratios:   append([]float64(nil), p.Ratios...),
		seed:     seed,
		stratify: p.Stratify,
		orderBy:  p.OrderBy,
	}, nil
}

func (c *trainTestSplit) Compute(records []Record, _ string) (map[string]Output, error) {
	if len(records) == 0 {
		return map[string]Output{c.label: {Values: []float64{}}}, nil
	}

	if c.orderBy != "" {
		return c.computeTemporal(records), nil
	}

	values := make([]float64, len(records))

	if c.stratify == "" {
		c.assignBucket(rangeIndices(len(records)), values)
		return map[string]Output{c.label: {Values: values}}, nil
	}

	groups := make(map[string][]int, 16)
	for i, r := range records {
		s, ok, err := r.LevelValueChecked(c.stratify)
		if err != nil {
			return nil, err
		}
		if !ok {
			s = ""
		}
		groups[s] = append(groups[s], i)
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		c.assignBucket(groups[k], values)
	}
	return map[string]Output{c.label: {Values: values}}, nil
}

// PrePass records the row's stratify key (when configured) or just
// counts the row. The stratifyKeys slice grows with iteration order so
// Finalize can rebuild the same per-group index lists Compute uses.
func (c *trainTestSplit) PrePass(r Record, _ string) error {
	c.rowCount++
	if c.stratify == "" {
		return nil
	}
	s, ok, err := r.LevelValueChecked(c.stratify)
	if err != nil {
		return err
	}
	if !ok {
		s = ""
	}
	c.stratifyKeys = append(c.stratifyKeys, s)
	return nil
}

// Finalize materializes the per-row split assignment, mirroring the
// buffered Compute branch. Memory is O(rows) — same as the buffered
// path's output slice.
func (c *trainTestSplit) Finalize() error {
	if c.orderBy != "" {
		// featstream routes temporal mode buffered; reaching here is a
		// dispatch bug, never a silent random split.
		return errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"FEAT_TRAIN_TEST_SPLIT: temporal mode runs buffered, never on the streaming pre-pass")
	}
	c.assignments = make([]float64, c.rowCount)
	if c.rowCount == 0 {
		return nil
	}
	if c.stratify == "" {
		c.assignBucket(rangeIndices(c.rowCount), c.assignments)
		return nil
	}
	groups := make(map[string][]int, 16)
	for i, k := range c.stratifyKeys {
		groups[k] = append(groups[k], i)
	}
	c.stratifyKeys = nil
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		c.assignBucket(groups[k], c.assignments)
	}
	return nil
}

// EmitRow returns the precomputed split assignment for the next record
// in iteration order. Records must be consumed in the same order as
// PrePass observed them; the streaming dispatcher guarantees this via
// iter.Reset().
func (c *trainTestSplit) EmitRow(_ Record, _ string) (map[string]Output, error) {
	if c.emitIdx >= len(c.assignments) {
		return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
			"FEAT_TRAIN_TEST_SPLIT: EmitRow called more times than PrePass observed")
	}
	v := c.assignments[c.emitIdx]
	c.emitIdx++
	return map[string]Output{c.label: {Values: []float64{v}}}, nil
}

// assignBucket shuffles indices using the operator's seed (each call adds a
// per-group offset so stratified groups don't collapse to identical
// orderings) and assigns each index to a bucket per the configured ratios.
func (c *trainTestSplit) assignBucket(indices []int, out []float64) {
	if len(indices) == 0 {
		return
	}
	shuffled := append([]int(nil), indices...)
	r := rand.New(rand.NewSource(c.seed + int64(len(out))*int64(indices[0]+1)))
	r.Shuffle(len(shuffled), func(i, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})

	n := len(shuffled)
	trainEnd := int(math.Round(float64(n) * c.ratios[0]))
	valEnd := trainEnd
	if len(c.ratios) >= 2 {
		valEnd = trainEnd + int(math.Round(float64(n)*c.ratios[1]))
	}
	if valEnd > n {
		valEnd = n
	}

	for k, idx := range shuffled {
		switch {
		case k < trainEnd:
			out[idx] = SplitTrain
		case k < valEnd:
			out[idx] = SplitVal
		default:
			out[idx] = SplitTest
		}
	}
}

// computeTemporal is the temporal split (mode "temporal"). Placeable rows
// (a non-null, non-NaN order_by) are sorted by their order value and cut
// by the ratios exactly as the random split cuts its shuffle — train the
// first round(n·r0), val the next round(n·r1), test the rest, n the
// placeable count — except that each boundary then snaps to the nearest
// edge between two distinct order values (on a tie, the earlier edge),
// so rows sharing an order value always land in one partition. A row
// whose order_by is null (or NaN) has no place in time: its split value
// is null, so it joins no partition and a `split == 0` filter or fit_on
// never admits it. Integer arithmetic over the sorted positions: the
// assignment is a function of the order values alone, never of record
// order or worker count.
func (c *trainTestSplit) computeTemporal(records []Record) map[string]Output {
	n := len(records)
	values := make([]float64, n)
	var nulls []bool
	order := make([]float64, n)
	placed := make([]int, 0, n)
	for i, r := range records {
		v, ok := r.NumericValue(c.orderBy)
		if !ok || math.IsNaN(v) {
			if nulls == nil {
				nulls = make([]bool, n)
			}
			nulls[i] = true
			continue
		}
		order[i] = v
		placed = append(placed, i)
	}
	sort.SliceStable(placed, func(a, b int) bool { return order[placed[a]] < order[placed[b]] })

	m := len(placed)
	// edges are the sorted positions where a new order value starts,
	// plus m: every place a boundary may fall without splitting a tie.
	edges := []int{0}
	for k := 1; k < m; k++ {
		if order[placed[k]] != order[placed[k-1]] {
			edges = append(edges, k)
		}
	}
	if m > 0 {
		edges = append(edges, m)
	}
	trainEnd := int(math.Round(float64(m) * c.ratios[0]))
	valEnd := trainEnd + int(math.Round(float64(m)*c.ratios[1]))
	if valEnd > m {
		valEnd = m
	}
	trainEnd, valEnd = snapEdge(edges, trainEnd), snapEdge(edges, valEnd)
	for k, idx := range placed {
		switch {
		case k < trainEnd:
			values[idx] = SplitTrain
		case k < valEnd:
			values[idx] = SplitVal
		default:
			values[idx] = SplitTest
		}
	}
	return map[string]Output{c.label: {Values: values, Nulls: nulls}}
}

// snapEdge returns the edge nearest b (edges sorted ascending, non-empty);
// equidistant edges resolve to the earlier one.
func snapEdge(edges []int, b int) int {
	j := sort.SearchInts(edges, b)
	if j < len(edges) && edges[j] == b {
		return b
	}
	if j == 0 {
		return edges[0]
	}
	if j == len(edges) {
		return edges[len(edges)-1]
	}
	if b-edges[j-1] <= edges[j]-b {
		return edges[j-1]
	}
	return edges[j]
}

func rangeIndices(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}
