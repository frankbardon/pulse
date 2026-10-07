package descriptor

import (
	"maps"
	"slices"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/returnplan"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// predict_return_sizes.go is the predict-time size model behind
// ReturnPlan.Sizes and the Open-include note behind
// ReturnPlan.UnresolvedIncludes. Header + schema only: every count comes
// from the request, the schema's dictionaries (vectors.EstimateBuckets,
// the rule MatrixPredict already reports) and the record count derived
// from the file length — never a record.
//
// Each section is a small tree of sizeNodes mirroring its JSON shape.
// Its full size is the tree's sum; its shaped size walks the same tree
// through the plan's Visit, dropping every node the plan drops — so
// shaped <= full by construction and an excluded section is 0.

// Size bases (ReturnSectionSize.Basis).
const (
	returnSizeExact      = "exact"
	returnSizeUpperBound = "upper_bound"
	returnSizeHeuristic  = "heuristic"
)

// The bytes-per-value model of compact JSON. Guidance only.
const (
	sizeKeyOverhead = 4  // the key's quotes, the colon, the separator
	sizeNumber      = 12 // a float or count, typical width
	sizeLabel       = 16 // a short string value, quotes included
	sizeObject      = 2  // an element's braces / brackets
	sizeDetails     = 96 // a test's open details map
	sizeOperatorMap = 64 // one component operator map
	sizeHeader      = 64 // an axis header object
	sizeMetaFile    = 32 // metadata.cohort_file
)

// sizeNode is one node of a section's size tree: a key or array-element
// step repeated count times, carrying its own bytes plus its children.
type sizeNode struct {
	seg   returnplan.Segment
	count int64
	own   int64
	kids  []*sizeNode
}

// leaf is a scalar key: the key text plus its value bytes.
func leaf(name string, value int64) *sizeNode {
	return &sizeNode{seg: returnplan.Key(name), count: 1, own: int64(len(name)) + sizeKeyOverhead + value}
}

// key is an object / array-valued key over kids.
func key(name string, kids ...*sizeNode) *sizeNode {
	return &sizeNode{seg: returnplan.Key(name), count: 1, own: int64(len(name)) + sizeKeyOverhead + sizeObject, kids: kids}
}

// elems is n array elements, each an object of kids.
func elems(n int64, kids ...*sizeNode) *sizeNode {
	return &sizeNode{seg: returnplan.Elem(), count: n, own: sizeObject, kids: kids}
}

// elemValues is n scalar array elements of value bytes each.
func elemValues(n, value int64) *sizeNode {
	return &sizeNode{seg: returnplan.Elem(), count: n, own: value + 1}
}

func (n *sizeNode) full() int64 {
	sum := n.own
	for _, k := range n.kids {
		sum += k.full()
	}
	return n.count * sum
}

// shaped is the node's size under plan, its parent at path. A node the
// plan drops is 0; a whole-kept node is its full size; a partially kept
// node keeps its own bytes and asks for each child.
func (n *sizeNode) shaped(plan *returnplan.Plan, path []returnplan.Segment) int64 {
	c := append(append(make([]returnplan.Segment, 0, len(path)+1), path...), n.seg)
	v := plan.Visit(c)
	if !v.Keep {
		return 0
	}
	if v.Whole || len(n.kids) == 0 {
		return n.full()
	}
	sum := n.own
	for _, k := range n.kids {
		sum += k.shaped(plan, c)
	}
	return n.count * sum
}

// returnSizeInputs is what the section models read.
type returnSizeInputs struct {
	req      *types.Request
	schema   *encoding.Schema
	inst     *InstanceSnapshot
	matrices []descriptor.MatrixPredict
	// records is the cohort's record count; recordsKnown is false when
	// it cannot be derived from the file length.
	records      int64
	recordsKnown bool
	// components reports that Response.Components is computed.
	components bool
}

// clamp bounds a bucket estimate by the record count: a run cannot emit
// more non-empty buckets than it has records.
func (in returnSizeInputs) clamp(n int64) int64 {
	if in.recordsKnown && n > in.records {
		return in.records
	}
	return n
}

// dataRows is the Response.Data row count, its basis, and whether it is
// known at all.
func (in returnSizeInputs) dataRows() (int64, string, bool) {
	req := in.req
	if len(req.Groups) > 0 {
		b, _, known := vectors.EstimateBuckets(req.Groups, in.schema)
		if !known {
			return 0, "", false
		}
		return in.clamp(b), returnSizeUpperBound, true
	}
	if len(req.Aggregations) > 0 {
		return 1, returnSizeExact, true
	}
	if !in.recordsKnown {
		return 0, "", false
	}
	return in.records, returnSizeUpperBound, true
}

// crosstabCells is the crosstab's row and column counts (the product of
// each axis's per-grouper key counts) and whether both are known.
func (in returnSizeInputs) crosstabCells() (rows, cols int64, ok bool) {
	axis := func(gs []*types.Group) (int64, bool) {
		n := int64(1)
		for _, g := range gs {
			b, _, known := vectors.EstimateBuckets([]*types.Group{g}, in.schema)
			if !known {
				return 0, false
			}
			n *= b
		}
		return n, true
	}
	r, rok := axis(in.req.Crosstab.Rows)
	c, cok := axis(in.req.Crosstab.Columns)
	return r, c, rok && cok
}

// returnSection is one section's tree and basis.
type returnSection struct {
	node  *sizeNode
	basis string
}

// returnSections models every top-level section the request produces
// and predict can size, in Response key order.
func returnSections(in returnSizeInputs) []returnSection {
	req := in.req
	var out []returnSection
	add := func(n *sizeNode, basis string) { out = append(out, returnSection{n, basis}) }

	rows, rowsBasis, rowsKnown := in.dataRows()
	var xtRows, xtCols int64
	xtKnown := false
	if req.Crosstab != nil {
		xtRows, xtCols, xtKnown = in.crosstabCells()
	}

	// data: rows x output columns. A crosstab writes no data rows; a
	// join's column set is open.
	if req.Crosstab == nil && rowsKnown {
		if cols, open := returnOutputColumns(req, in.schema, in.inst); !open {
			kids := make([]*sizeNode, 0, len(cols))
			for _, c := range slices.Sorted(maps.Keys(cols)) {
				kids = append(kids, leaf(c, sizeNumber))
			}
			add(key("data", elems(rows, kids...)), rowsBasis)
		}
	}

	add(key("metadata", leaf("total_rows", sizeNumber), leaf("filtered_rows", sizeNumber),
		leaf("cohort_file", sizeMetaFile)), returnSizeHeuristic)

	testNode := func(name string, n int) *sizeNode {
		return key(name, elems(int64(n),
			leaf("label", sizeLabel), leaf("type", sizeLabel), leaf("variant", sizeLabel),
			leaf("statistic", sizeNumber), leaf("df", sizeNumber), leaf("p_value", sizeNumber),
			leaf("alpha", sizeNumber), leaf("reject_null", 5),
			leaf("details", sizeDetails)))
	}
	if len(req.Tests) > 0 {
		add(testNode("tests", len(req.Tests)), returnSizeHeuristic)
	}
	if len(req.PostTests) > 0 {
		add(testNode("post_tests", len(req.PostTests)), returnSizeHeuristic)
	}

	if len(req.Regressions) > 0 {
		var kids []*sizeNode
		for _, r := range req.Regressions {
			if r == nil {
				continue
			}
			terms := int64(len(r.Predictors)) + 1
			// A map of one entry per term (the intercept included).
			term := func(name string) *sizeNode {
				return leaf(name, sizeObject+terms*(sizeLabel+1+sizeNumber+1))
			}
			kids = append(kids, elems(1,
				leaf("name", sizeLabel), leaf("type", sizeLabel),
				term("coefficients"), term("std_errors"), term("p_values"),
				leaf("r2", sizeNumber), leaf("adj_r2", sizeNumber), leaf("n_obs", sizeNumber)))
		}
		add(key("regressions", kids...), returnSizeHeuristic)
	}

	// matrices: one result per spec per bucket, p x p cells each.
	if len(in.matrices) > 0 {
		var kids []*sizeNode
		basis := returnSizeExact
		known := true
		for _, m := range in.matrices {
			if m.EstimatedBuckets == nil {
				known = false
				break
			}
			if m.BucketBasis != vectors.BucketBasisUngrouped {
				basis = returnSizeUpperBound
			}
			p := int64(m.Shape[0])
			values := func(name string) *sizeNode {
				return key(name, leaf("kind", sizeLabel), leaf("encoding", sizeLabel),
					key("row_keys", elemValues(p, sizeLabel)), key("column_keys", elemValues(p, sizeLabel)),
					key("values", elemValues(p*p, sizeNumber)))
			}
			kids = append(kids, elems(in.clamp(*m.EstimatedBuckets),
				leaf("name", sizeLabel), leaf("type", sizeLabel), leaf("group_key", sizeLabel),
				values("primary"), key("vectors", elemValues(p, sizeNumber)),
				leaf("scalars", sizeNumber)))
		}
		if known {
			add(key("matrices", kids...), basis)
		}
	}

	// crosstab: the dense matrix over the axes' key counts.
	if req.Crosstab != nil && xtKnown {
		cell := int64(sizeNumber + 1)
		add(key("crosstab",
			leaf("shape", sizeLabel),
			key("matrix",
				leaf("row_header", sizeHeader), leaf("column_header", sizeHeader),
				key("row_keys", elemValues(xtRows, sizeLabel)), key("column_keys", elemValues(xtCols, sizeLabel)),
				key("cells", elemValues(xtRows, xtCols*cell+sizeObject)),
				key("row_margins", elemValues(xtRows, cell)), key("column_margins", elemValues(xtCols, cell)),
				leaf("grand_total", sizeNumber), leaf("cell_label", sizeLabel), leaf("normalize_applied", sizeLabel)),
		), returnSizeUpperBound)
	}

	// overlays: one layer per spec, one payload value per host cell.
	if len(req.Overlays) > 0 {
		host, hostKnown := rows, rowsKnown
		if req.Crosstab != nil {
			host, hostKnown = xtRows*xtCols, xtKnown
		}
		if hostKnown {
			layer := elems(int64(len(req.Overlays)),
				leaf("name", sizeLabel), leaf("kind", sizeLabel), leaf("scope", sizeLabel),
				leaf("ref", sizeHeader), leaf("payload", host*(sizeNumber+1)), leaf("summary", sizeHeader))
			add(key("overlays", layer), returnSizeHeuristic)
		}
	}

	// components: floors per slot, per group when grouped, per cell
	// under a crosstab.
	if in.components && (len(req.Groups) == 0 || rowsKnown) && (req.Crosstab == nil || xtKnown) {
		var kids []*sizeNode
		if n := len(req.Aggregations); n > 0 {
			slot := []*sizeNode{leaf("n", sizeNumber), leaf("n_null", sizeNumber), leaf("operator", sizeOperatorMap)}
			if len(req.Groups) > 0 {
				slot = append(slot, key("groups", elems(rows, leaf("key", sizeLabel),
					leaf("n", sizeNumber), leaf("n_null", sizeNumber), leaf("operator", sizeOperatorMap))))
			}
			kids = append(kids, key("aggregations", elems(int64(n), slot...)))
		}
		if n := len(req.Groups); n > 0 {
			kids = append(kids, key("groupers", elems(int64(n), leaf("field", sizeLabel),
				leaf("total_n", sizeNumber), leaf("n_null", sizeNumber), leaf("operator", sizeOperatorMap))))
		}
		if req.Crosstab != nil {
			kids = append(kids, key("crosstab",
				key("cell_counts", elemValues(xtRows, xtCols*(sizeNumber+1)+sizeObject)),
				key("cell_components", elemValues(xtRows, xtCols*(sizeOperatorMap+1)+sizeObject)),
				key("row_margin_counts", elemValues(xtRows, sizeNumber)),
				key("column_margin_counts", elemValues(xtCols, sizeNumber)),
				leaf("grand_total_count", sizeNumber)))
		}
		if n := len(req.Filterers); n > 0 {
			kids = append(kids, key("filterers", elems(int64(n), leaf("n_in", sizeNumber),
				leaf("n_out", sizeNumber), leaf("n_null_input", sizeNumber))))
		}
		kids = append(kids, key("run", leaf("total_records", sizeNumber),
			leaf("filtered_records", sizeNumber), leaf("null_records", sizeNumber)))
		add(key("components", kids...), returnSizeHeuristic)
	}
	return out
}

// returnSizes is ReturnPlan.Sizes: every modelled section, full and
// under plan.
func returnSizes(plan *returnplan.Plan, in returnSizeInputs) []descriptor.ReturnSectionSize {
	if plan == nil || in.req == nil || in.schema == nil {
		return nil
	}
	sections := returnSections(in)
	out := make([]descriptor.ReturnSectionSize, 0, len(sections))
	for _, s := range sections {
		out = append(out, descriptor.ReturnSectionSize{
			Section:     s.node.seg.Name,
			FullBytes:   s.node.full(),
			ShapedBytes: s.node.shaped(plan, nil),
			Basis:       s.basis,
		})
	}
	return out
}

// unresolvedIncludes is ReturnPlan.UnresolvedIncludes: the plan's Open
// include paths, less every `data[*].<column>` include predict matches
// against the request's known (closed) output column set.
func unresolvedIncludes(plan *returnplan.Plan, req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot) []string {
	if plan == nil {
		return nil
	}
	var cols map[string]bool
	closed := false
	if req != nil && schema != nil {
		var open bool
		cols, open = returnOutputColumns(req, schema, inst)
		closed = !open
	}
	var out []string
	for _, in := range plan.Include {
		if !in.Open {
			continue
		}
		if closed && matchesDataColumn(in, cols) {
			continue
		}
		out = append(out, in.String())
	}
	return out
}

// matchesDataColumn reports whether p is `data[*].<seg>` and selects
// one of cols.
func matchesDataColumn(p returnplan.Path, cols map[string]bool) bool {
	if len(p.Segments) < 3 {
		return false
	}
	s0, s1 := p.Segments[0], p.Segments[1]
	if s0.Name != "data" || s0.Glob || s0.Index || !s1.Index {
		return false
	}
	for c := range cols {
		if p.Selects([]returnplan.Segment{returnplan.Key("data"), returnplan.Elem(), returnplan.Key(c)}) {
			return true
		}
	}
	return false
}
