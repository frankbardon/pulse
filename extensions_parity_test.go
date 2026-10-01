package pulse_test

// Built-in ⇔ extension parity harness.
//
// Every extension category authored against the public extend package
// must produce byte-identical output to the built-in operator it
// reimplements. The harness is a table of paritySuite entries, each
// carrying one extension registration plus rows of {builtin request,
// extension request}; every row runs through every parityMode (the
// cohort × execution-mode matrix) and the two arms must agree on
// Response.Data, Response.Components, Response.Metadata and the full
// ProcessStreamResult chunk sequence. Later categories add a suite —
// the modes and the cohort fixture are shared.
//
// Fixture values are chosen so every float sum is exact (integers and
// halves), so a parallel arm that folds in a different order than a
// serial arm still agrees bit-for-bit.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// ---------------------------------------------------------------------
// Cohort fixture: one schema carrying every field family the matrix
// covers — nullable numeric, plain numeric, categorical, narrow set,
// both wide set rungs (one nullable), nullable decimal128, date and
// datetime.
// ---------------------------------------------------------------------

const (
	parityW128Members = 80  // bits ≥ 64 force the second mask word
	parityW256Members = 210 // bits ≥ 192 force the fourth mask word
)

func parityDict(t testing.TB, prefix string, n int) *encoding.Dictionary {
	t.Helper()
	d := encoding.NewDictionary()
	for i := 0; i < n; i++ {
		if _, err := d.Add(fmt.Sprintf("%s%03d", prefix, i)); err != nil {
			t.Fatalf("dict.Add: %v", err)
		}
	}
	return d
}

func paritySchema(t testing.TB) *encoding.Schema {
	t.Helper()
	region := encoding.NewDictionary()
	for _, l := range []string{"north", "south", "east", "west"} {
		if _, err := region.Add(l); err != nil {
			t.Fatalf("dict.Add: %v", err)
		}
	}
	fields := []encoding.Field{
		{Name: "score", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "qty", Type: encoding.FieldTypeU32},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: region},
		{Name: "tags", Type: encoding.FieldTypeSetU8, Dictionary: parityDict(t, "t", 5)},
		{Name: "w128", Type: encoding.FieldTypeSetU128, Nullable: true, Dictionary: parityDict(t, "a", parityW128Members)},
		{Name: "w256", Type: encoding.FieldTypeSetU256, Dictionary: parityDict(t, "b", parityW256Members)},
		{Name: "amount", Type: encoding.FieldTypeDecimal128, Nullable: true, Precision: 18, Scale: 2},
		{Name: "day", Type: encoding.FieldTypeDate},
		{Name: "ts", Type: encoding.FieldTypeDateTime},
	}
	off := 0
	for i := range fields {
		fields[i].ByteOffset = off
		fields[i].CsvColumnIdx = i
		off += fields[i].Type.ByteSize()
	}
	return &encoding.Schema{Fields: fields}
}

func maskBits(bits ...int) encoding.SetMask {
	var w [encoding.SetMaskWords]uint64
	for _, b := range bits {
		w[b/64] |= 1 << (b % 64)
	}
	return encoding.SetMaskFromWords(w)
}

// parityPayload encodes rows [from, to) of the deterministic fixture.
func parityPayload(t testing.TB, s *encoding.Schema, from, to int) []byte {
	t.Helper()
	stride := s.RecordByteSize()
	bm := s.BitmapByteSize()
	out := make([]byte, 0, (to-from)*stride)
	for r := from; r < to; r++ {
		rec := make([]byte, stride)
		bitmap := rec[stride-bm:]
		f := s.Fields
		le := func(off int, v uint64, n int) {
			for i := 0; i < n; i++ {
				rec[off+i] = byte(v >> (8 * i))
			}
		}
		// score: nullable f64, exact halves.
		if r%7 == 3 {
			encoding.BitmapSetNull(bitmap, 0)
		} else {
			le(f[0].ByteOffset, math.Float64bits(float64(r%50)+0.5), 8)
		}
		le(f[1].ByteOffset, uint64(r%13), 4)
		rec[f[2].ByteOffset] = byte(r % 4)
		rec[f[3].ByteOffset] = byte(1<<(r%5) | 1<<((r*3+1)%5))
		if r%11 == 5 {
			encoding.BitmapSetNull(bitmap, 4)
		} else if err := encoding.PutSetMask(rec[f[4].ByteOffset:], f[4].Type, maskBits(r%3, 70+r%4)); err != nil {
			t.Fatalf("PutSetMask w128: %v", err)
		}
		if err := encoding.PutSetMask(rec[f[5].ByteOffset:], f[5].Type, maskBits(r%2, 200+r%3)); err != nil {
			t.Fatalf("PutSetMask w256: %v", err)
		}
		if r%9 == 4 {
			encoding.BitmapSetNull(bitmap, 6)
		} else {
			enc := encoding.EncodeDecimal128(encoding.NewDecimal128FromInt(int64((r%97)*100 + r%100)))
			copy(rec[f[6].ByteOffset:], enc[:])
		}
		le(f[7].ByteOffset, uint64(19000+r%6), 4)
		le(f[8].ByteOffset, uint64(1_700_000_000+(r%40)*7200), 8)
		out = append(out, rec...)
	}
	return out
}

func writeParityCohort(t testing.TB, fsys afero.Fs, path string, s *encoding.Schema, from, to int) {
	t.Helper()
	var buf []byte
	w := &byteSink{b: &buf}
	if err := encoding.WriteHeader(w); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(w, s); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	buf = append(buf, parityPayload(t, s, from, to)...)
	if err := afero.WriteFile(fsys, path, buf, 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

type byteSink struct{ b *[]byte }

func (s *byteSink) Write(p []byte) (int, error) { *s.b = append(*s.b, p...); return len(p), nil }

// ---------------------------------------------------------------------
// Mode matrix.
// ---------------------------------------------------------------------

// parityMode is one execution arm. open returns a Pulse configured for
// the mode plus the cohort path; decorate (optional) adds request
// slots that force the mode's path on BOTH arms; extPath, when set, is
// the path the extension operator must have been driven through
// ("online" or "buffered") so a silent fallback cannot pass parity.
type parityMode struct {
	name     string
	open     func(t *testing.T, ext pulse.Extensions) (*pulse.Pulse, string)
	decorate func(*types.Request) *types.Request
	extPath  string
	// mergeable: the built-in arm of every streamable row must be
	// mergeable, otherwise the parallel / per-shard path never engages
	// and the mode silently degrades to serial.
	mergeable bool
}

const (
	paritySmallRows = 96
	// parityLargeRows clears the engine's 100K-record parallel decode
	// threshold (internal/service.parallelDecodeRecordThreshold).
	parityLargeRows = 100_000 + 4096
)

// parityLargeCohort returns a lazy getter for the >threshold fixture,
// written at most once under dir on the REAL filesystem: parallel
// decode needs mmap via RealPath and bails silently on a MemMapFs.
func parityLargeCohort(dir string) func(t *testing.T) string {
	var once sync.Once
	return func(t *testing.T) string {
		t.Helper()
		once.Do(func() {
			writeParityCohort(t, afero.NewOsFs(), filepath.Join(dir, "large.pulse"),
				paritySchema(t), 0, parityLargeRows)
		})
		return dir
	}
}

func parityModes(large func(*testing.T) string) []parityMode {
	small := func(t *testing.T, ext pulse.Extensions) (*pulse.Pulse, string) {
		fsys := afero.NewMemMapFs()
		writeParityCohort(t, fsys, "parity.pulse", paritySchema(t), 0, paritySmallRows)
		p, err := pulse.New(pulse.Options{FS: fsys, Extensions: ext})
		if err != nil {
			t.Fatalf("pulse.New: %v", err)
		}
		return p, "parity.pulse"
	}
	return []parityMode{
		{name: "streaming", open: small, extPath: "online"},
		{
			name: "buffered",
			open: small,
			// ATTR_PERCENTILE is non-streamable and contributes no
			// output column to an aggregation response: it forces the
			// buffered path on both arms without changing the payload.
			decorate: func(r *types.Request) *types.Request {
				c := *r
				c.Attributes = append(append([]*types.Attribute(nil), r.Attributes...),
					&types.Attribute{Type: types.ATTR_PERCENTILE, Field: "qty", Label: "qty_pct"})
				return &c
			},
			extPath: "buffered",
		},
		{
			name: "parallel_buffered",
			open: func(t *testing.T, ext pulse.Extensions) (*pulse.Pulse, string) {
				dir := large(t)
				p, err := pulse.New(pulse.Options{DataDir: dir, DecodeWorkers: 4, Extensions: ext})
				if err != nil {
					t.Fatalf("pulse.New: %v", err)
				}
				return p, "large.pulse"
			},
			mergeable: true,
		},
		{
			name: "shard_archive",
			open: func(t *testing.T, ext pulse.Extensions) (*pulse.Pulse, string) {
				fsys := afero.NewMemMapFs()
				s := paritySchema(t)
				shards := []string{"s0.pulse", "s1.pulse", "s2.pulse"}
				for i, path := range shards {
					writeParityCohort(t, fsys, path, s, i*40, (i+1)*40)
				}
				p, err := pulse.New(pulse.Options{FS: fsys, ShardWorkers: 3, Extensions: ext})
				if err != nil {
					t.Fatalf("pulse.New: %v", err)
				}
				if _, err := p.CreateShardArchive(context.Background(), "archive.pulse", shards); err != nil {
					t.Fatalf("CreateShardArchive: %v", err)
				}
				return p, "archive.pulse"
			},
			mergeable: true,
		},
	}
}

// ---------------------------------------------------------------------
// Suites: one per extension category. E2 adds the remaining categories.
// ---------------------------------------------------------------------

// parityProbe counts which path the engine drove the extension
// operator through.
type parityProbe struct {
	online, buffered atomic.Int64
	// decimalReads counts DecimalValue hits that returned a value.
	decimalReads atomic.Int64
}

func (p *parityProbe) reset() { p.online.Store(0); p.buffered.Store(0); p.decimalReads.Store(0) }

type parityRow struct {
	name      string
	builtin   *types.Request
	extension *types.Request
	// bufferedOnly: the built-in shape never streams (a non-streamable
	// grouper, …), so both arms run buffered in every mode. Cross-
	// checked against processing.CanStreamRequest so the flag cannot
	// drift from the engine.
	bufferedOnly bool
	// serialOnly: the built-in shape is not mergeable, so the parallel
	// and per-shard modes run it serially. Cross-checked against
	// processing.CanMergeRequest — an unflagged row that stops merging
	// would otherwise silently degrade those modes to serial.
	serialOnly bool
	// mustContain lists substrings the built-in Data JSON must carry,
	// so a row cannot pass on two identically degenerate payloads (a
	// wide-set fold truncated to the low mask word, say).
	mustContain []string
}

type paritySuite struct {
	name     string
	register func(*parityProbe) pulse.Extensions
	rows     []parityRow
}

func paritySuites() []paritySuite {
	return []paritySuite{aggregatorParitySuite()}
}

// ---------------------------------------------------------------------
// Aggregator: AGG_PARITY_SUM, an extend reimplementation of AGG_SUM.
// ---------------------------------------------------------------------

const aggParitySum types.AggregationType = "AGG_PARITY_SUM"

// paritySum mirrors the built-in AGG_SUM: nulls are skipped, numerics
// fold through NumericValue, and a decimal128 target sums exactly via
// DecimalValue and renders through Rich at the field's scale (the
// built-in's decimal result marshals as the same quoted string).
type paritySum struct {
	probe   *parityProbe
	sum     float64
	isDec   bool
	scale   uint8
	dec     encoding.Decimal128
	decErr  error
	touched bool
}

var (
	_ extend.OnlineAggregator = (*paritySum)(nil)
	_ extend.RichAggregator   = (*paritySum)(nil)
)

func (a *paritySum) add(r extend.Record, field string) {
	if a.isDec {
		if d, ok := r.DecimalValue(field); ok {
			a.probe.decimalReads.Add(1)
			next, err := a.dec.Add(d)
			if err != nil {
				a.decErr = err
				return
			}
			a.dec = next
		}
		return
	}
	if v, ok := r.NumericValue(field); ok {
		a.sum += v
	}
}

func (a *paritySum) Aggregate(rows extend.Rows, field string) (float64, error) {
	a.probe.buffered.Add(1)
	for i := 0; i < rows.Len(); i++ {
		a.add(rows.At(i), field)
	}
	return a.sum, a.decErr
}

func (a *paritySum) UpdateRow(r extend.Record, field string) error {
	if !a.touched {
		a.touched = true
		a.probe.online.Add(1)
	}
	a.add(r, field)
	return a.decErr
}

func (a *paritySum) Finalize() (float64, error) { return a.sum, a.decErr }

func (a *paritySum) Rich() (any, error) {
	if !a.isDec {
		return nil, nil
	}
	return a.dec.String(a.scale), nil
}

func aggregatorParitySuite() paritySuite {
	sum := func(t types.AggregationType, field string) []*types.Aggregation {
		return []*types.Aggregation{{Type: t, Field: field, Label: "total"}}
	}
	// row builds a {builtin, extension} pair that differs only in the
	// aggregation type.
	row := func(name, field string, groups []*types.Group, filters []*types.Filterer) parityRow {
		return parityRow{
			name:      name,
			builtin:   &types.Request{Aggregations: sum(types.AGG_SUM, field), Groups: groups, Filterers: filters},
			extension: &types.Request{Aggregations: sum(aggParitySum, field), Groups: groups, Filterers: filters},
		}
	}
	// GROUP_DATE is neither streamable nor mergeable.
	nonStreaming := func(r parityRow) parityRow { r.bufferedOnly, r.serialOnly = true, true; return r }
	contains := func(r parityRow, subs ...string) parityRow { r.mustContain = subs; return r }
	by := func(gt types.GroupType, field string) []*types.Group {
		return []*types.Group{{Type: gt, Field: field}}
	}
	return paritySuite{
		name: "aggregator",
		register: func(probe *parityProbe) pulse.Extensions {
			return pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{{
				Name:        aggParitySum,
				Description: "Test-only extend reimplementation of AGG_SUM.",
				Factory: func(spec *types.Aggregation, schema *encoding.Schema) (extend.Aggregator, error) {
					a := &paritySum{probe: probe, dec: encoding.ZeroDecimal128()}
					if schema != nil && spec != nil {
						if f := schema.Field(spec.Field); f != nil && f.Type.IsDecimal() {
							a.isDec, a.scale = true, f.Scale
						}
					}
					return a, nil
				},
				Streamable: true,
				Accepts: []encoding.FieldType{
					encoding.FieldTypeF64, encoding.FieldTypeU32, encoding.FieldTypeDecimal128,
				},
				FieldInputs: func(json.RawMessage) []string { return nil },
				ComponentSchema: descriptor.ComponentSchema{
					Keys:         []descriptor.ComponentKey{{Name: "sum", Type: "float64", Description: "Running sum."}},
					Mergeability: descriptor.Mergeable,
				},
				ComponentsFunc: func(inst extend.Aggregator) (map[string]any, error) {
					a := inst.(*paritySum)
					if a.isDec {
						// The built-in decimal path emits the universal
						// floor only; mirror it.
						return nil, nil
					}
					return map[string]any{"sum": a.sum}, nil
				},
			}}}
		},
		rows: []parityRow{
			row("nullable_f64", "score", nil, nil),
			row("numeric_u32", "qty", nil, nil),
			row("categorical", "score", by(types.GROUP_CATEGORY, "region"), nil),
			row("narrow_set", "qty", by(types.GROUP_SET_PER_ELEMENT, "tags"), nil),
			contains(row("wide_set_u128", "score", by(types.GROUP_SET_PER_ELEMENT, "w128"), nil), "a070", "a073"),
			contains(row("wide_set_u256", "qty", by(types.GROUP_SET_PER_ELEMENT, "w256"), nil), "b200", "b202"),
			// decimal128 targets force the buffered, serial path on both
			// arms (CanStreamRequest / CanMergeRequest refuse them); the
			// twin sums exactly via DecimalValue and renders via Rich.
			contains(nonStreaming(row("decimal128", "amount", nil, nil)), `"total":"`),
			contains(nonStreaming(row("decimal128_grouped", "amount", by(types.GROUP_CATEGORY, "region"), nil)), `"total":"`),
			nonStreaming(row("date", "score", by(types.GROUP_DATE, "day"), nil)),
			nonStreaming(row("datetime", "qty", by(types.GROUP_DATE, "ts"), nil)),
			row("filtered", "score", by(types.GROUP_CATEGORY, "region"),
				[]*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "region", Values: []string{"north", "west"}}}),
		},
	}
}

// ---------------------------------------------------------------------
// The harness.
// ---------------------------------------------------------------------

type parityOutcome struct {
	data, components, metadata string
	chunks                     []string
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return string(b)
}

func runParityArm(t *testing.T, p *pulse.Pulse, path string, req *types.Request) parityOutcome {
	t.Helper()
	ctx := context.Background()
	r := *req
	r.Cohort = &types.Cohort{Filename: path}
	resp, err := p.Process(ctx, &r)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(resp.Data) == 0 {
		t.Fatal("Process returned no rows; parity over an empty payload proves nothing")
	}
	out := parityOutcome{
		data:       mustJSON(t, resp.Data),
		components: mustJSON(t, resp.Components),
		metadata:   mustJSON(t, resp.Metadata),
	}
	sr, err := p.ProcessStreamResult(ctx, &r)
	if err != nil {
		t.Fatalf("ProcessStreamResult: %v", err)
	}
	for c := range sr.Chunks {
		out.chunks = append(out.chunks, mustJSON(t, c))
	}
	if done := <-sr.Done; done.Status != pulse.StreamCompleted {
		t.Fatalf("stream status = %v, err = %v", done.Status, done.Error)
	}
	return out
}

func assertParity(t *testing.T, builtin, ext parityOutcome) {
	t.Helper()
	if builtin.data != ext.data {
		t.Errorf("Response.Data differs\nbuiltin:   %s\nextension: %s", builtin.data, ext.data)
	}
	if builtin.components != ext.components {
		t.Errorf("Response.Components differ\nbuiltin:   %s\nextension: %s", builtin.components, ext.components)
	}
	if builtin.metadata != ext.metadata {
		t.Errorf("Response.Metadata differs\nbuiltin:   %s\nextension: %s", builtin.metadata, ext.metadata)
	}
	if len(builtin.chunks) != len(ext.chunks) {
		t.Fatalf("stream chunk count: builtin %d, extension %d", len(builtin.chunks), len(ext.chunks))
	}
	for i := range builtin.chunks {
		if builtin.chunks[i] != ext.chunks[i] {
			t.Errorf("stream chunk %d differs\nbuiltin:   %s\nextension: %s", i, builtin.chunks[i], ext.chunks[i])
		}
	}
}

// TestExtensions_BuiltinParity runs every suite's {builtin, extension}
// pair through the cohort × mode matrix and requires identical output.
func TestExtensions_BuiltinParity(t *testing.T) {
	large := parityLargeCohort(t.TempDir())
	for _, suite := range paritySuites() {
		for _, mode := range parityModes(large) {
			t.Run(suite.name+"/"+mode.name, func(t *testing.T) {
				probe := &parityProbe{}
				p, path := mode.open(t, suite.register(probe))
				schema := paritySchema(t)
				for _, row := range suite.rows {
					t.Run(row.name, func(t *testing.T) {
						bReq, eReq := row.builtin, row.extension
						if mode.decorate != nil {
							bReq, eReq = mode.decorate(bReq), mode.decorate(eReq)
						}
						if mode.decorate == nil {
							if got := processing.CanStreamRequest(bReq, schema); got == row.bufferedOnly {
								t.Fatalf("CanStreamRequest(builtin) = %v but row.bufferedOnly = %v", got, row.bufferedOnly)
							}
						}
						if mode.mergeable {
							if got := processing.CanMergeRequest(bReq, schema); got == row.serialOnly {
								t.Fatalf("CanMergeRequest(builtin) = %v but row.serialOnly = %v; mode %s would not run the path it names",
									got, row.serialOnly, mode.name)
							}
						}
						builtin := runParityArm(t, p, path, bReq)
						probe.reset()
						ext := runParityArm(t, p, path, eReq)
						assertParity(t, builtin, ext)
						for _, sub := range row.mustContain {
							if !strings.Contains(builtin.data, sub) {
								t.Errorf("built-in Data lacks %q: %s", sub, builtin.data)
							}
						}
						want := mode.extPath
						if row.bufferedOnly {
							want = "buffered"
						}
						switch want {
						case "online":
							if probe.online.Load() == 0 || probe.buffered.Load() != 0 {
								t.Errorf("extension path: online=%d buffered=%d, want online only",
									probe.online.Load(), probe.buffered.Load())
							}
						case "buffered":
							if probe.buffered.Load() == 0 || probe.online.Load() != 0 {
								t.Errorf("extension path: online=%d buffered=%d, want buffered only",
									probe.online.Load(), probe.buffered.Load())
							}
						}
					})
				}
			})
		}
	}
}

// TestExtensions_DecimalTarget asserts a registered extension
// aggregator over a decimal128 field runs — the engine does not refuse
// it against the built-in decimal table — and reaches
// extend.Record.DecimalValue on the aggregation's own field, through
// Process (buffered), ProcessStream and a crosstab cell. The built-in
// refusal of a non-decimal built-in (AGG_MEDIAN) stays exactly as it
// was. The cross-mode output parity lives in aggregatorParitySuite's
// decimal128 row.
func TestExtensions_DecimalTarget(t *testing.T) {
	probe := &parityProbe{}
	fsys := afero.NewMemMapFs()
	writeParityCohort(t, fsys, "parity.pulse", paritySchema(t), 0, paritySmallRows)
	p, err := pulse.New(pulse.Options{FS: fsys, Extensions: aggregatorParitySuite().register(probe)})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	ctx := context.Background()
	req := func(at types.AggregationType) *types.Request {
		return &types.Request{
			Cohort:       &types.Cohort{Filename: "parity.pulse"},
			Aggregations: []*types.Aggregation{{Type: at, Field: "amount", Label: "total"}},
		}
	}

	builtin, err := p.Process(ctx, req(types.AGG_SUM))
	if err != nil {
		t.Fatalf("built-in AGG_SUM over decimal128: %v", err)
	}
	ext, err := p.Process(ctx, req(aggParitySum))
	if err != nil {
		t.Fatalf("extension over decimal128 (Process): %v", err)
	}
	if probe.buffered.Load() == 0 || probe.decimalReads.Load() == 0 {
		t.Fatalf("extension did not reach DecimalValue: buffered=%d decimalReads=%d",
			probe.buffered.Load(), probe.decimalReads.Load())
	}
	if b, e := mustJSON(t, builtin.Data), mustJSON(t, ext.Data); b != e {
		t.Errorf("Process Data differs\nbuiltin:   %s\nextension: %s", b, e)
	}

	probe.reset()
	streamChunks := func(at types.AggregationType) []string {
		sr, err := p.ProcessStreamResult(ctx, req(at))
		if err != nil {
			t.Fatalf("ProcessStreamResult(%s) over decimal128: %v", at, err)
		}
		var out []string
		for c := range sr.Chunks {
			out = append(out, mustJSON(t, c))
		}
		if done := <-sr.Done; done.Status != pulse.StreamCompleted {
			t.Fatalf("ProcessStreamResult(%s): status = %v, err = %v", at, done.Status, done.Error)
		}
		return out
	}
	if e := streamChunks(aggParitySum); probe.decimalReads.Load() == 0 {
		t.Fatal("ProcessStreamResult: extension did not reach DecimalValue")
	} else if b := streamChunks(types.AGG_SUM); strings.Join(b, "\n") != strings.Join(e, "\n") {
		t.Errorf("stream chunks differ\nbuiltin:   %v\nextension: %v", b, e)
	}

	probe.reset()
	xt := func(at types.AggregationType) *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: "parity.pulse"},
			Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Columns: []*types.Group{{Type: types.GROUP_DATE, Field: "day"}},
				Cell:    &types.Aggregation{Type: at, Field: "amount", Label: "total"},
			},
		}
	}
	bx, err := p.Process(ctx, xt(types.AGG_SUM))
	if err != nil {
		t.Fatalf("built-in crosstab over decimal128: %v", err)
	}
	ex, err := p.Process(ctx, xt(aggParitySum))
	if err != nil {
		t.Fatalf("extension crosstab over decimal128: %v", err)
	}
	if probe.decimalReads.Load() == 0 {
		t.Fatal("crosstab: extension did not reach DecimalValue")
	}
	if b, e := mustJSON(t, bx.Crosstab), mustJSON(t, ex.Crosstab); b != e {
		t.Errorf("crosstab differs\nbuiltin:   %s\nextension: %s", b, e)
	}

	// A built-in with no decimal implementation is still refused.
	_, err = p.Process(ctx, req(types.AGG_MEDIAN))
	var ce *perr.CodedError
	if !errors.As(err, &ce) || ce.Code != perr.PROCESSING_CONFIG {
		t.Fatalf("built-in AGG_MEDIAN over decimal128: err = %v, want PROCESSING_CONFIG", err)
	}
	_, err = p.Process(ctx, xt(types.AGG_MEDIAN))
	if !errors.As(err, &ce) || ce.Code != perr.PROCESSING_CONFIG {
		t.Fatalf("built-in AGG_MEDIAN crosstab over decimal128: err = %v, want PROCESSING_CONFIG", err)
	}
}

// TestExtensions_DecimalTargetPredict asserts predict recognises a
// registered extension aggregator on a decimal128 field: no
// PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL warning, no strict error and no
// replace-operator suggestion. A built-in without a decimal
// implementation still warns (and errors under Strict).
func TestExtensions_DecimalTargetPredict(t *testing.T) {
	var buf []byte
	w := &byteSink{b: &buf}
	if err := encoding.WriteHeader(w); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(w, paritySchema(t)); err != nil {
		t.Fatal(err)
	}
	buf = append(buf, parityPayload(t, paritySchema(t), 0, 8)...)
	code := string(perr.PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL)
	has := func(entries []*descriptor.EnvelopeEntry) bool {
		for _, e := range entries {
			if e.Code == code {
				return true
			}
		}
		return false
	}
	for _, strict := range []bool{false, true} {
		p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Strict: strict,
			Extensions: aggregatorParitySuite().register(&parityProbe{})})
		if err != nil {
			t.Fatalf("pulse.New: %v", err)
		}
		predict := func(at types.AggregationType) *descriptor.Envelope {
			env, err := p.PredictBytes(context.Background(), buf, &types.Request{
				Aggregations: []*types.Aggregation{{Type: at, Field: "amount", Label: "total"}},
			})
			if err != nil {
				t.Fatalf("PredictBytes: %v", err)
			}
			return env
		}
		env := predict(aggParitySum)
		if has(env.Warnings) || has(env.Errors) {
			t.Errorf("strict=%v: extension over decimal128 flagged %s: warnings=%v errors=%v",
				strict, code, env.Warnings, env.Errors)
		}
		if res, ok := env.Data.(*descriptor.PredictResult); ok {
			for _, s := range res.Suggestions {
				if s.Current == string(aggParitySum) && strings.Contains(s.Reason, "ecimal") {
					t.Errorf("strict=%v: extension over decimal128 drew suggestion %+v", strict, s)
				}
			}
		} else {
			t.Fatalf("env.Data = %T, want *descriptor.PredictResult", env.Data)
		}
		env = predict(types.AGG_MEDIAN)
		if strict && !has(env.Errors) || !strict && !has(env.Warnings) {
			t.Errorf("strict=%v: built-in AGG_MEDIAN over decimal128 not flagged: warnings=%v errors=%v",
				strict, env.Warnings, env.Errors)
		}
	}
}

// BenchmarkAggregatorAdapted measures the adapted extend dispatch cost:
// AGG_PARITY_SUM (an extend.Aggregator behind the root adapter) against
// the built-in AGG_SUM over the same cohort. "streaming" is an
// ungrouped sum (online fold); "buffered" groups by a non-streamable
// GROUP_DATE so both arms materialise rows and call Aggregate.
func BenchmarkAggregatorAdapted(b *testing.B) {
	const rows = 50_000
	fsys := afero.NewMemMapFs()
	writeParityCohort(b, fsys, "bench.pulse", paritySchema(b), 0, rows)
	p, err := pulse.New(pulse.Options{FS: fsys, Extensions: aggregatorParitySuite().register(&parityProbe{})})
	if err != nil {
		b.Fatalf("pulse.New: %v", err)
	}
	shapes := []struct {
		name   string
		groups []*types.Group
	}{
		{"streaming", nil},
		{"buffered", []*types.Group{{Type: types.GROUP_DATE, Field: "day"}}},
	}
	for _, shape := range shapes {
		for _, arm := range []struct {
			name string
			agg  types.AggregationType
		}{{"builtin", types.AGG_SUM}, {"extension", aggParitySum}} {
			req := &types.Request{
				Cohort:       &types.Cohort{Filename: "bench.pulse"},
				Groups:       shape.groups,
				Aggregations: []*types.Aggregation{{Type: arm.agg, Field: "score", Label: "total"}},
			}
			b.Run(shape.name+"/"+arm.name, func(b *testing.B) {
				ctx := context.Background()
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					if _, err := p.Process(ctx, req); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
