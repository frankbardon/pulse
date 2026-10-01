package synth

import (
	"bytes"
	"fmt"
	"io"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
)

// RunContinuationHighThreshold is the per-field rate at or above which
// RunContinuationProfile.HighFields lists a field: a field that repeats
// its previous row's bytes on at least three adjacent pairs in four is
// one the run-skip decode almost never rewrites.
const RunContinuationHighThreshold = 0.75

// runContinuationLowOverall is the overall rate below which the advice
// says the run-skip decode will mostly back off. The reader stops
// comparing when more than half of a probe window's fields changed, so
// an overall rate under one half is the regime where it pays full decode.
const runContinuationLowOverall = 0.5

// RunContinuationProfile is the measured run-continuation of a cohort
// (`pulse profile create --run-continuation`,
// ProfileOptions.RunContinuation): per field, the fraction of adjacent
// row pairs whose on-wire bytes for that field — and, for a nullable
// field, its null bit — are identical.
//
// It is the exact per-field hit rate of the run-skip decode
// (internal/encoding/reader_runskip.go), which rewrites only the fields whose
// bytes or null bit changed since the previous row. Because the
// optimisation is a property of the DATA rather than the format, a
// cohort re-imported without its upstream ORDER BY loses it with nothing
// on the wire saying so; this section is how it becomes visible.
//
// Pairs never span a shard boundary. Each shard of an archive is a
// separately written payload and the decode restarts its comparison
// baseline at each one, so a pair across two shards measures nothing the
// optimisation can use. Shards counts the payloads scanned (1 for a
// single-file cohort); Pairs is the sum over shards of (rows − 1).
//
// Additive and omitempty: absent from every document captured without
// the flag. SpecFromProfile never reads it — it describes the row ORDER
// of the source, which generation does not reproduce.
type RunContinuationProfile struct {
	// Pairs is the number of within-shard adjacent row pairs compared.
	Pairs int `json:"pairs"`
	// Shards is the number of payloads scanned.
	Shards int `json:"shards"`
	// Overall is the mean of the per-field rates — the fraction of all
	// (pair, field) comparisons that repeated, i.e. the share of field
	// writes the run-skip decode avoids on a full-row read.
	Overall float64 `json:"overall"`
	// HighThreshold is the rate at or above which a field is listed in
	// HighFields (RunContinuationHighThreshold).
	HighThreshold float64 `json:"high_threshold"`
	// HighFields lists, in schema order, the fields at or above
	// HighThreshold: the high-continuation block a sort key holds
	// constant (on a denormalised join, the parent block).
	HighFields []string `json:"high_fields"`
	// Advice is a one-sentence reading of Overall for a user deciding
	// whether to sort the source upstream.
	Advice string `json:"advice"`
	// Fields carries every schema field's rate, in schema order.
	Fields []FieldContinuation `json:"fields"`
}

// FieldContinuation is one field's run-continuation rate.
type FieldContinuation struct {
	Name string  `json:"name"`
	Rate float64 `json:"rate"`
}

// rowSource reads one whole record stride at a time from a segment and
// serves it to the decode from memory, so the scan that decodes a row
// also holds that row's raw on-wire bytes for the continuation
// accumulator — no second read, and one read call per row instead of
// one per field.
//
// buf holds the PHYSICAL row (what the decode reads). row is what the
// continuation accumulator compares: the physical row itself for an
// ungrouped cohort, the expanded LOGICAL row for a grouped (0x02) one —
// the bytes the run-skip decode actually compares, field by field.
type rowSource struct {
	buf []byte
	br  bytes.Reader
	x   *encx.RowExpander
	row []byte
}

func newRowSource(schema *encoding.Schema) (*rowSource, error) {
	s := &rowSource{buf: make([]byte, schema.RecordByteSize())}
	if schema.HasGroups() {
		x, err := encx.NewRowExpander(schema)
		if err != nil {
			return nil, err
		}
		s.x = x
	}
	return s, nil
}

// next loads the segment's next row. A clean end and a truncated final
// row both return io.EOF, exactly as the field-by-field decode treats
// them.
func (s *rowSource) next(r io.Reader) error {
	if _, err := io.ReadFull(r, s.buf); err != nil {
		if err == io.ErrUnexpectedEOF {
			return io.EOF
		}
		return err
	}
	s.br.Reset(s.buf)
	if s.x == nil {
		s.row = s.buf
		return nil
	}
	row, err := s.x.Expand(s.row, s.buf)
	if err != nil {
		return err
	}
	s.row = row
	return nil
}

// continuationAcc counts, per field, the adjacent row pairs whose bytes
// and null bit repeat. Its geometry is a pure function of the schema:
// one on-wire span per field (a bit-packed field consumes one whole
// byte, decimal128 sixteen, every other type its fixed width) followed
// by the null bitmap — of the LOGICAL row, so a grouped cohort's
// members are measured from their expanded bytes.
type continuationAcc struct {
	schema *encoding.Schema
	off    []int
	width  []int
	stride int
	bmOff  int // -1 when the schema carries no bitmap
	prev   []byte
	have   bool
	same   []int
	pairs  int
	shards int
}

func newContinuationAcc(schema *encoding.Schema) *continuationAcc {
	schema = schema.Logical()
	a := &continuationAcc{
		schema: schema,
		off:    make([]int, len(schema.Fields)),
		width:  make([]int, len(schema.Fields)),
		same:   make([]int, len(schema.Fields)),
		bmOff:  -1,
		shards: 1,
	}
	off := 0
	for i := range schema.Fields {
		w := 1
		if !schema.Fields[i].Type.IsBitPacked() {
			w = schema.Fields[i].Type.ByteSize()
		}
		a.off[i], a.width[i] = off, w
		off += w
	}
	if schema.HasBitmap() {
		a.bmOff = off
	}
	a.stride = schema.RecordByteSize()
	a.prev = make([]byte, 0, a.stride)
	return a
}

// observe folds one decoded row's raw bytes. A row whose captured
// length is not the schema stride is a reader-geometry disagreement the
// accumulator must not paper over.
func (a *continuationAcc) observe(row []byte) error {
	if len(row) != a.stride {
		return fmt.Errorf("run continuation: row carried %d bytes, schema stride is %d", len(row), a.stride)
	}
	if a.have {
		a.pairs++
		for i := range a.off {
			o, w := a.off[i], a.width[i]
			if string(row[o:o+w]) != string(a.prev[o:o+w]) {
				continue
			}
			if a.bmOff >= 0 && a.schema.Fields[i].Nullable &&
				encoding.BitmapIsNull(row[a.bmOff:], i) != encoding.BitmapIsNull(a.prev[a.bmOff:], i) {
				continue
			}
			a.same[i]++
		}
	}
	a.prev = append(a.prev[:0], row...)
	a.have = true
	return nil
}

// boundary starts a new shard: the next row is compared against
// nothing.
func (a *continuationAcc) boundary() {
	a.have = false
	a.shards++
}

func (a *continuationAcc) finish() *RunContinuationProfile {
	out := &RunContinuationProfile{
		Pairs:         a.pairs,
		Shards:        a.shards,
		HighThreshold: RunContinuationHighThreshold,
		HighFields:    []string{},
		Fields:        make([]FieldContinuation, len(a.same)),
	}
	total := 0
	for i, s := range a.same {
		rate := 0.0
		if a.pairs > 0 {
			rate = float64(s) / float64(a.pairs)
		}
		name := a.schema.Fields[i].Name
		out.Fields[i] = FieldContinuation{Name: name, Rate: rate}
		if a.pairs > 0 && rate >= RunContinuationHighThreshold {
			out.HighFields = append(out.HighFields, name)
		}
		total += s
	}
	if a.pairs > 0 && len(a.same) > 0 {
		out.Overall = float64(total) / float64(a.pairs*len(a.same))
	}
	out.Advice = continuationAdvice(out, len(a.same))
	return out
}

func continuationAdvice(p *RunContinuationProfile, fields int) string {
	switch {
	case p.Pairs == 0:
		return "no shard holds two rows, so there are no adjacent pairs to measure"
	case p.Overall < runContinuationLowOverall:
		return fmt.Sprintf("low: adjacent rows repeat %.0f%% of field bytes (%d of %d fields at or above %.2f), "+
			"so the run-skip decode mostly backs off and scans pay full decode; if this cohort is a "+
			"denormalised parent/child join, sort the source by the parent key upstream (ORDER BY) and "+
			"re-import so each parent's block repeats across its child rows",
			100*p.Overall, len(p.HighFields), fields, p.HighThreshold)
	default:
		return fmt.Sprintf("high: adjacent rows repeat %.0f%% of field bytes (%d of %d fields at or above %.2f), "+
			"so the run-skip decode skips most field writes; keep the upstream ORDER BY that produces "+
			"this order when re-importing or appending shards",
			100*p.Overall, len(p.HighFields), fields, p.HighThreshold)
	}
}
