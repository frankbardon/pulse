package encoding

import "github.com/frankbardon/pulse/encoding"

// ExactU64Words carries the exact on-wire word of every u64 field one
// MAP decode (ReadRecordWithWideProjected / ReadRecordWithWidePlan /
// ReadRecordAtExact) retained, beside the float64 echo it wrote into the
// values map. The echo loses every bit past float64's 53-bit mantissa;
// a caller that converts the maps into an ExactU64Record (the engine's
// positional record) hands it the words with ApplyTo, so the record's
// exact u64 accessor reads the stored value on the map-decode paths
// exactly as it does on the reuse decoders (which write the word
// themselves through SetExactU64At).
//
// A null u64 field keeps no word. Install it on a reader with
// RecordReader.CaptureExactU64; every map decode on that reader resets
// it first, so it always describes the last record decoded. The backing
// slices are reused across records: a warm capture allocates nothing.
//
// Not safe for concurrent use: one per reader.
type ExactU64Words struct {
	idx   []int
	words []uint64
}

// NewExactU64Words returns a capture for schema, or nil when the schema
// declares no u64 field — a nil capture is "off" everywhere, so a cohort
// without a u64 field pays nothing.
func NewExactU64Words(schema *encoding.Schema) *ExactU64Words {
	if schema == nil {
		return nil
	}
	for i := range schema.Fields {
		if schema.Fields[i].Type == encoding.FieldTypeU64 {
			return &ExactU64Words{}
		}
	}
	return nil
}

func (w *ExactU64Words) reset() {
	w.idx = w.idx[:0]
	w.words = w.words[:0]
}

func (w *ExactU64Words) put(fi int, v uint64) {
	w.idx = append(w.idx, fi)
	w.words = append(w.words, v)
}

// drop forgets field fi's word (the bitmap marked it null).
func (w *ExactU64Words) drop(fi int) {
	for i, x := range w.idx {
		if x == fi {
			last := len(w.idx) - 1
			w.idx[i], w.words[i] = w.idx[last], w.words[last]
			w.idx, w.words = w.idx[:last], w.words[:last]
			return
		}
	}
}

// ApplyTo hands every captured word to rec, keyed by schema field
// index. Call it after rec holds the decoded values (its SetExactU64At
// keeps a word only beside a value). A nil receiver is a no-op.
func (w *ExactU64Words) ApplyTo(rec ExactU64Record) {
	if w == nil {
		return
	}
	for i, fi := range w.idx {
		rec.SetExactU64At(fi, w.words[i])
	}
}

// CaptureExactU64 installs w on rr: every subsequent map decode records
// the exact word of each retained, non-null u64 field into it. nil turns
// capture off. The reuse decoders ignore it (they write the word into
// their record directly).
func (rr *RecordReader) CaptureExactU64(w *ExactU64Words) { rr.exact = w }
