package processing

import (
	"fmt"
	"math"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/mergegate"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// CanChainRequest reports whether a request is eligible to participate
// in a ProcessChain stage — CanChainRequestWithExtensions with a nil
// registry (built-in operators only). A request qualifies iff it
// passes the mergeable gate (CanMergeRequest), i.e. its online state
// can be merged across partitions of the input. Every mergeable built-in aggregator emits one float64 per output row,
// so no further scalar check is needed: AGG_MODE's modal value (on a
// categorical field the dictionary index, not the label), AGG_MODE_COUNT's
// modal count and AGG_FREQUENCY's value count are all admitted.
//
// The chain executor calls this before each stage. A failing stage
// surfaces PULSE_CHAIN_NOT_MERGEABLE with the stage index in details.
func CanChainRequest(req *types.Request, schema *encoding.Schema) bool {
	return CanChainRequestWithExtensions(req, schema, nil)
}

// CanChainRequestWithExtensions is CanChainRequest against a registry
// that knows embedder-registered operators: the merge gate is
// CanMergeRequestWithExtensions, so a Mergeable extension aggregator is
// admitted. An extension aggregator whose value is Rich emits a
// non-scalar cell that the next stage's f64 column reads as null —
// the same posture as the built-in set aggregators. A nil exts is
// exactly CanChainRequest.
func CanChainRequestWithExtensions(req *types.Request, schema *encoding.Schema, exts *ExtensionRegistry) bool {
	return ChainRefusal(req, schema, exts, 0, "") == nil
}

// ChainRefusal is the coded form of CanChainRequestWithExtensions — the
// error ProcessChain returns for a stage that fails the gate:
// PULSE_CHAIN_NOT_MERGEABLE "chain stage is not mergeable: <reason>"
// {stage_index, stage_name}, nil when the stage passes. It is
// internal/mergegate.ChainRefusal over this registry; the chain
// validator calls the same function over the ExtensionsSnapshot, so
// both sides refuse with one code, message and details.
func ChainRefusal(req *types.Request, schema *encoding.Schema, exts *ExtensionRegistry, stageIndex int, stageName string) error {
	return mergegate.ChainRefusal(req, schema, exts.mergeFacts(), stageIndex, stageName)
}

// mergeFacts adapts the registry to mergegate.Extensions. Nil-safe: a
// nil registry answers ok=false for every name (built-ins only). A name
// the instance feature set hides answers (false, true) — known and not
// mergeable — which refuses it with the same reason the built-in tables
// give a never-registered name.
func (r *ExtensionRegistry) mergeFacts() mergegate.Extensions {
	return registryMergeFacts{r}
}

type registryMergeFacts struct{ r *ExtensionRegistry }

func (f registryMergeFacts) Aggregator(name string) (bool, bool) {
	if f.r.isHidden(name) {
		return false, true
	}
	if !f.r.isExtensionAggregator(types.AggregationType(name)) {
		return false, false
	}
	return f.r.IsMergeable("aggregator", name), true
}

func (f registryMergeFacts) Grouper(name string) (bool, bool) {
	if f.r.isHidden(name) {
		return false, true
	}
	if f.r == nil {
		return false, false
	}
	if _, ok := f.r.Groupers[types.GroupType(name)]; !ok {
		return false, false
	}
	return f.r.IsMergeable("grouper", name), true
}

func (f registryMergeFacts) Filterer(name string) (bool, bool) {
	if f.r.isHidden(name) {
		return false, true
	}
	if f.r == nil {
		return false, false
	}
	if _, ok := f.r.Filterers[types.FiltererType(name)]; !ok {
		return false, false
	}
	return f.r.IsStreamable("filterer", name), true
}

// Matrix answers for EXTENSION matrix operators (and hidden names): a
// co-moment-tier operator merges through the engine's blocked merge
// like a built-in, a buffered one keeps rows and does not.
func (f registryMergeFacts) Matrix(name string) (bool, bool) {
	if f.r.isHidden(name) {
		return false, true
	}
	op, ok := f.r.LookupMatrixOp(types.MatrixType(name))
	if !ok {
		return false, false
	}
	return op.Streamable, true
}

// Attribute answers only for EXTENSION attributes (and hidden names):
// row-local when declared row_local and — for one emitting
// Components.Attributes figures (ComponentAttributes) — declared
// Mergeable, its per-run tally folding through
// extend.MergeableAttribute (processing.MergeableAttributeTally);
// a built-in answers (false, false) so mergegate.MergeRefusal decides
// it per attribute — ATTR_FORMULA is row-local only without a
// total(expr) call (totalcall.Contains), or with one the reducers' two-
// phase drive covers (mergegate.ParallelTotals; never on a chain
// stage). The type-keyed two-pass check below is therefore sufficient:
// no extension attribute carries an expression-dependent tier. Pinned
// by TestFormulaTotal_MergeAdmission and
// TestChainGate_ValidatorMatchesRuntime ("total formula").
func (f registryMergeFacts) Attribute(name string) (bool, bool) {
	if f.r.isHidden(name) {
		return false, true
	}
	t := types.AttributeType(name)
	if !f.r.isExtensionAttribute(t) {
		return false, false
	}
	emitsUnmerged := f.r.ComponentAttributes[t] && !f.r.IsMergeable("attribute", name)
	return f.r.IsStreamable("attribute", name) && !f.r.attributeRequiresTwoPass(t) && !emitsUnmerged, true
}

// AggregationLabel returns the output column name for an aggregation,
// matching the labelling used by the streaming and buffered paths in
// Processor.Process. Empty label falls back to "<TYPE>_<Field>".
func AggregationLabel(agg *types.Aggregation) string {
	if agg.Label != "" {
		return agg.Label
	}
	return types.AggregationLabelOf(agg)
}

// ChainOutputSchema synthesises the schema produced by a mergeable
// chain stage req executing over its input schema in, under the
// instance's aggregation kinds. The returned schema is purely
// in-memory — no byte layout — and is intended for downstream stages
// constructed via SliceIterator over RecordsFromChainRows.
//
// Layout (vectors.ChainStageSchema — the builder predict's
// synthChainSchema shares, so both sides type the next stage alike):
//   - One categorical_u32 field per Group entry (named by its key
//     column, vectors.GroupKeyField — the Data row key the stage writes),
//     with an empty Dictionary that the caller populates as rows are
//     materialized.
//   - One f64 field per aggregation output column: its label
//     (AggregationLabel), or one "<label>[<element label>]" column per
//     member of an expanding vector aggregation (params.expand), whose
//     members resolve against in.
//
// Row-local attributes (FORMULA, DATE_PART) feed aggregators but do
// not surface in the streaming/grouped output today, so they do not
// appear in the chain schema. ByteOffset / BitPosition are zero
// across the board because the schema is never serialised. An
// aggregation column colliding with an earlier column is
// PROCESSING_CONFIG.
func ChainOutputSchema(req *types.Request, in *encoding.Schema, kinds vectors.AggregationKinds) (*encoding.Schema, error) {
	if req == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG, "chain output schema requires non-nil request")
	}
	schema, cerr := vectors.ChainStageSchema(req, in, kinds)
	if cerr != nil {
		return nil, cerr
	}
	return schema, nil
}

// RecordsFromChainRows materializes records for the next chain stage
// from a Response.Data slice + the synthesised schema: every record
// ChainRowIterator yields, at once. Categorical fields land in the
// dictionary as new keys arrive. Numeric cells are converted to
// float64; missing / nil cells — and a NaN, the undefined figure the
// wire writes as null (an expanded vector element no row answered, a
// ratio over a zero denominator) — become nulls.
//
// The returned records share the schema reference; the schema's
// dictionaries are mutated as new keys are observed. The chain runtime
// reads a stage's rows through ChainRowIterator instead, which holds
// no record beyond the one it yields.
func RecordsFromChainRows(rows []map[string]any, schema *encoding.Schema) ([]*Record, error) {
	it, err := NewChainRowIterator(rows, schema)
	if err != nil {
		return nil, err
	}
	out := make([]*Record, 0, len(rows))
	for it.Next() {
		out = append(out, it.Record())
	}
	return out, nil
}

// ChainRowIterator is the next chain stage's input over the previous
// stage's Response.Data rows (U51 E5-S12, #499). It builds each record
// when Next reaches its row and keeps none past the current one, so a
// stage's input costs a few bytes a row beside the rows the chain
// response returns anyway — materializing every record up front
// (RecordsFromChainRows) held one Record a row for the whole stage.
//
// NewChainRowIterator resolves every categorical cell once, in row
// order, so the schema's dictionaries are complete — and their entries
// in first-seen order — before the stage runs, exactly as the eager
// form left them; a dictionary refusal surfaces there. The records it
// yields are byte-for-byte RecordsFromChainRows' (same values, same
// nulls), and Reset replays them.
type ChainRowIterator struct {
	rows   []map[string]any
	schema *encoding.Schema
	// codes[c][row] is the dictionary index of categorical field
	// cats[c] in row, or -1 when the cell is null.
	cats  []int
	codes [][]int64
	pos   int
	cur   *Record
}

// NewChainRowIterator validates the schema's field types, resolves the
// categorical cells (populating the dictionaries) and returns the
// iterator positioned before the first row.
func NewChainRowIterator(rows []map[string]any, schema *encoding.Schema) (*ChainRowIterator, error) {
	if schema == nil {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG, "chain materialization requires schema")
	}
	it := &ChainRowIterator{rows: rows, schema: schema, pos: -1}
	// checked: the fields the pass below visits, in field order — every
	// categorical one, plus any type the chain schema never carries
	// (refused at the first row holding a value for it, as the eager
	// form did).
	var checked []int
	for i := range schema.Fields {
		switch schema.Fields[i].Type {
		case encoding.FieldTypeF64:
		case encoding.FieldTypeCategoricalU32:
			it.cats = append(it.cats, i)
			checked = append(checked, i)
		default:
			checked = append(checked, i)
		}
	}
	it.codes = make([][]int64, len(it.cats))
	for c := range it.cats {
		it.codes[c] = make([]int64, len(rows))
	}
	// Row-major, field order within a row: the order the eager form
	// added keys in (each field owns its dictionary, so only the row
	// order matters) and met a refusal in.
	for ri, row := range rows {
		c := 0
		for _, fi := range checked {
			f := &schema.Fields[fi]
			raw, present := row[f.Name]
			if f.Type != encoding.FieldTypeCategoricalU32 {
				if present && raw != nil {
					return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
						fmt.Sprintf("unsupported chain field type %s for %q", f.Type.String(), f.Name))
				}
				continue
			}
			it.codes[c][ri] = -1
			c++
			if !present || raw == nil {
				continue
			}
			key, ok := stringifyChainKey(raw)
			if !ok {
				continue
			}
			id, err := f.Dictionary.Add(key)
			if err != nil {
				return nil, errors.NewCodedError(errors.PROCESSING_RUNTIME,
					fmt.Sprintf("row %d field %q: %s", ri, f.Name, err.Error()))
			}
			it.codes[c-1][ri] = int64(id)
		}
	}
	return it, nil
}

// Next advances to the next row and builds its record.
func (it *ChainRowIterator) Next() bool {
	it.cur = nil
	if it.pos >= len(it.rows) {
		return false
	}
	it.pos++
	if it.pos >= len(it.rows) {
		return false
	}
	it.cur = it.build(it.pos)
	return true
}

// Record returns the current row's record (valid after Next returns
// true). Each row's record is a fresh value the caller may keep.
func (it *ChainRowIterator) Record() *Record { return it.cur }

// Reset rewinds to before the first row.
func (it *ChainRowIterator) Reset() {
	it.pos = -1
	it.cur = nil
}

// Len is the number of rows the iterator yields.
func (it *ChainRowIterator) Len() int { return len(it.rows) }

// build is row ri's record: a categorical field its resolved index, an
// f64 field its numeric cell, and null for a missing, nil,
// non-numeric or NaN cell.
func (it *ChainRowIterator) build(ri int) *Record {
	row := it.rows[ri]
	r := newPositionalRecord(it.schema)
	c := 0
	for i := range it.schema.Fields {
		f := &it.schema.Fields[i]
		if c < len(it.cats) && it.cats[c] == i {
			if id := it.codes[c][ri]; id >= 0 {
				r.putValue(f.Name, float64(id))
			} else {
				r.markNull(f.Name)
			}
			c++
			continue
		}
		raw, present := row[f.Name]
		if !present || raw == nil {
			r.markNull(f.Name)
			continue
		}
		v, ok := coerceFloat(raw)
		if !ok || math.IsNaN(v) {
			r.markNull(f.Name)
			continue
		}
		r.putValue(f.Name, v)
	}
	return r
}

// stringifyChainKey converts any JSON-shaped value to a string for
// categorical lookup. Mirrors the streaming-path category grouper
// which stores keys as strings in its bucket map.
func stringifyChainKey(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case fmt.Stringer:
		return x.String(), true
	case float64:
		return fmt.Sprintf("%g", x), true
	case float32:
		return fmt.Sprintf("%g", float64(x)), true
	case int:
		return fmt.Sprintf("%d", x), true
	case int64:
		return fmt.Sprintf("%d", x), true
	case int32:
		return fmt.Sprintf("%d", x), true
	case uint64:
		return fmt.Sprintf("%d", x), true
	case uint32:
		return fmt.Sprintf("%d", x), true
	case bool:
		if x {
			return "true", true
		}
		return "false", true
	}
	return "", false
}

// coerceFloat reads any JSON-shaped numeric into float64. Returns
// (0, false) for nil / unrecognised / non-numeric values so the caller
// can mark the cell null.
func coerceFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case int32:
		return float64(x), true
	case uint64:
		return float64(x), true
	case uint32:
		return float64(x), true
	}
	return 0, false
}
