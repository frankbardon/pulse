package encoding

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	stderrors "errors"
	"io"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// distinct_trailer_faults_test.go pins the edges of the DIST trailer
// (U73 #500) and the schema doc that carries it: the writer's refusals,
// the reader's answers to every malformed length, the counter's
// refusals of a damaged shard, the lookup / bound accessors' "unknown"
// answers, and the HLL estimator once registers saturate.

// errAfterReader serves r, then fails instead of reporting EOF.
func errAfterReader(b []byte) io.Reader {
	return io.MultiReader(bytes.NewReader(b), iotestErrReader{})
}

type iotestErrReader struct{}

func (iotestErrReader) Read([]byte) (int, error) { return 0, errInjected }

func TestDistinctStatus_String(t *testing.T) {
	t.Parallel()
	for s, want := range map[DistinctStatus]string{
		DistinctAbsent: "absent", DistinctExact: "exact", DistinctOverLimit: "over_limit",
		DistinctOptedOut: "opted_out", DistinctStatus(9): "status(9)",
	} {
		if got := s.String(); got != want {
			t.Errorf("DistinctStatus(%d).String() = %q, want %q", uint8(s), got, want)
		}
	}
}

// TestArchiveDistinct_UnknownAnswers: every accessor answers "nothing
// known" — never a fabricated count or bound — for a nil trailer, an
// untracked field or an unlisted shard; and a bound whose sum passes
// 2^64 saturates instead of wrapping.
func TestArchiveDistinct_UnknownAnswers(t *testing.T) {
	t.Parallel()

	var nilD *ArchiveDistinct
	if nilD.VouchedBy(func(string) (ShardIdentity, bool) { return ShardIdentity{}, true }) {
		t.Error("a nil trailer vouched for its shards")
	}
	if got := nilD.Lookup("a", "id"); got != (DistinctCount{}) {
		t.Errorf("nil Lookup = %+v, want absent", got)
	}
	d := sampleDistinct()
	if got := d.Lookup("a.pulse", "nope"); got != (DistinctCount{}) {
		t.Errorf("untracked field Lookup = %+v, want absent", got)
	}
	if got := d.Lookup("z.pulse", "id"); got != (DistinctCount{}) {
		t.Errorf("unlisted shard Lookup = %+v, want absent", got)
	}
	if got := d.Lookup("a.pulse", "id"); got != (DistinctCount{DistinctExact, 3}) {
		t.Errorf("listed Lookup = %+v, want exact 3", got)
	}

	sat := &ArchiveDistinct{
		Fields: []DistinctField{{Name: "id", Type: encoding.FieldTypeU64}},
		Shards: []ShardDistinct{
			{Name: "a", Counts: []DistinctCount{{DistinctExact, math.MaxUint64 - 1}}},
			{Name: "b", Counts: []DistinctCount{{DistinctExact, 5}}},
			{Name: "c", Counts: []DistinctCount{{DistinctExact, 7}}},
		},
	}
	if got, ok := sat.FieldBound("id"); !ok || got != math.MaxUint64 {
		t.Errorf("overflowing bound = %d, %v; want MaxUint64, true", got, ok)
	}
}

// TestDistinctTrailer_WriterRefusals: a name past 65535 bytes (field or
// shard) or a shard whose counts do not match the field list is
// ENCODING_INVALID before anything is written; a failing sink at the
// header or the body is ENCODING_IO.
func TestDistinctTrailer_WriterRefusals(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", 0x10000)
	longField := sampleDistinct()
	longField.Fields[0].Name = long
	longShard := sampleDistinct()
	longShard.Shards[1].Name = long
	mismatch := sampleDistinct()
	mismatch.Shards[0].Counts = mismatch.Shards[0].Counts[:1]
	for name, d := range map[string]*ArchiveDistinct{"long field name": longField, "long shard name": longShard, "counts mismatch": mismatch} {
		var buf bytes.Buffer
		if err := writeDistinctTrailer(&buf, d); !errors.HasCode(err, errors.ENCODING_INVALID) || buf.Len() != 0 {
			t.Errorf("%s: err = %v after %d bytes, want ENCODING_INVALID before any byte", name, err, buf.Len())
		}
	}
	for n := 1; n <= 2; n++ {
		w := &nthFailWriter{n: n}
		if err := writeDistinctTrailer(w, sampleDistinct()); !errors.HasCode(err, errors.ENCODING_IO) || !stderrors.Is(err, errInjected) {
			t.Errorf("sink failing at write %d: %v, want ENCODING_IO", n, err)
		}
	}
}

// trailer frames body as a version-2 DIST trailer with the given
// declared body length.
func trailer(bodyLen uint64, body []byte) []byte {
	b := append([]byte(nil), DistinctTrailerMagic[:]...)
	b = append(b, DistinctTrailerVersion)
	b = binary.LittleEndian.AppendUint64(b, bodyLen)
	return append(b, body...)
}

// TestDistinctTrailer_ReaderLengthRefusals: an implausible body length,
// a field or shard count the body cannot hold, a body too short for its
// first counts or names, and a source that fails (rather than ends) are
// all ENCODING_INVALID — never an allocation sized by a lying count.
func TestDistinctTrailer_ReaderLengthRefusals(t *testing.T) {
	t.Parallel()

	u32 := func(v uint32) []byte { return binary.LittleEndian.AppendUint32(nil, v) }
	for name, r := range map[string]io.Reader{
		"magic unreadable":       iotestErrReader{},
		"implausible body":       bytes.NewReader(trailer(1<<41, nil)),
		"body unreadable":        errAfterReader(trailer(64, []byte{1, 2, 3})),
		"field count past body":  bytes.NewReader(trailer(8, append(u32(1000), 0, 0, 0, 0))),
		"shard count past body":  bytes.NewReader(trailer(8, append(u32(0), u32(1000)...))),
		"body too short for u32": bytes.NewReader(trailer(2, []byte{0, 0})),
		"field name cut short":   bytes.NewReader(trailer(4, u32(1))),
	} {
		d, err := readDistinctTrailer(r)
		if d != nil || !errors.HasCode(err, errors.ENCODING_INVALID) {
			t.Errorf("%s: %+v, %v; want nil, ENCODING_INVALID", name, d, err)
		}
	}
	// The well-formed empty trailer (no fields, no shards) reads.
	if d, err := readDistinctTrailer(bytes.NewReader(trailer(8, append(u32(0), u32(0)...)))); err != nil || d == nil || len(d.Fields)+len(d.Shards) != 0 {
		t.Errorf("empty trailer = %+v, %v; want an empty trailer", d, err)
	}
}

// TestReadSchemaDocDistinct_Edges: a doc without SHRD has no trailer; a
// bad header is an error; a trailer listing another shard count than
// SHRD is ENCODING_INVALID; a source that fails right after the schema
// block is ENCODING_INVALID, never "no extension".
func TestReadSchemaDocDistinct_Edges(t *testing.T) {
	t.Parallel()

	s := &encoding.Schema{Fields: []encoding.Field{{Name: "id", Type: encoding.FieldTypeU32}}}
	var plain bytes.Buffer
	if err := WritePreamble(&plain, s); err != nil {
		t.Fatal(err)
	}
	doc, d, err := ReadSchemaDocDistinct(bytes.NewReader(plain.Bytes()))
	if err != nil || doc == nil || d != nil || doc.ShardCount != 0 {
		t.Errorf("doc without SHRD = %+v, %+v, %v; want a doc and no trailer", doc, d, err)
	}
	if doc, d, err := ReadSchemaDocDistinct(strings.NewReader("not a pulse file")); doc != nil || d != nil || err == nil {
		t.Errorf("bad header = %+v, %+v, %v; want an error", doc, d, err)
	}

	without := docBytes(t, nil)
	with := docBytes(t, sampleDistinct())
	shrdCount := append([]byte(nil), with...)
	binary.LittleEndian.PutUint16(shrdCount[len(without)-2:], 3) // SHRD says 3, the trailer lists 2
	if _, _, err := ReadSchemaDocDistinct(bytes.NewReader(shrdCount)); !errors.HasCode(err, errors.ENCODING_INVALID) {
		t.Errorf("SHRD / trailer shard count mismatch: %v, want ENCODING_INVALID", err)
	}
	if _, err := ReadSchemaDoc(errAfterReader(plain.Bytes())); !errors.HasCode(err, errors.ENCODING_INVALID) || !stderrors.Is(err, errInjected) {
		t.Errorf("source failing after the schema block: %v, want ENCODING_INVALID wrapping the fault", err)
	}
}

// TestDistinctCounter_DamagedShardRefused: a shard whose header or
// schema block is damaged is PULSE_SHARD_HEADER_INVALID; a source that
// fails (rather than ends) mid-records is ENCODING_IO — a short tail is
// the only thing that floors silently.
func TestDistinctCounter_DamagedShardRefused(t *testing.T) {
	t.Parallel()

	s := &encoding.Schema{Fields: []encoding.Field{{Name: "id", Type: encoding.FieldTypeU64}}}
	fields := EligibleDistinctFields(s)
	payload := distinctPayload(t, s, [][]*uint64{{u(1)}, {u(2)}, {u(3)}})
	var pre bytes.Buffer
	if err := WritePreamble(&pre, s); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		r    io.Reader
		code errors.Code
	}{
		"bad header":         {strings.NewReader("garbage that is no pulse header"), errors.PULSE_SHARD_HEADER_INVALID},
		"truncated schema":   {bytes.NewReader(pre.Bytes()[:encoding.HeaderSize+2]), errors.PULSE_SHARD_HEADER_INVALID},
		"records unreadable": {errAfterReader(payload[:pre.Len()+8]), errors.ENCODING_IO},
	} {
		if got, err := CountShardDistinct(c.r, s, fields, 0); got != nil || !errors.HasCode(err, c.code) {
			t.Errorf("%s: %+v, %v; want nil, %s", name, got, err, c.code)
		}
	}
	// A short tail floors: the last partial record is not counted.
	got, err := CountShardDistinct(bytes.NewReader(payload[:len(payload)-3]), s, fields, 0)
	if err != nil || got[0] != (DistinctCount{DistinctExact, 2}) {
		t.Errorf("short tail = %+v, %v; want exact 2", got, err)
	}
}

// TestPeekShardRecordCount_MissingAndDeflatedFaults: an unknown entry is
// PULSE_SHARD_MISSING; a deflated entry is drained to count (its payload
// length is not in the directory), so one whose bytes fail their CRC-32
// is ENCODING_IO rather than a count of corrupt bytes.
func TestPeekShardRecordCount_MissingAndDeflatedFaults(t *testing.T) {
	t.Parallel()

	s := &encoding.Schema{Fields: []encoding.Field{{Name: "id", Type: encoding.FieldTypeU64}}}
	payload := distinctPayload(t, s, [][]*uint64{{u(1)}, {u(2)}, {u(3)}, {u(4)}})
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "a.pulse", Method: zip.Deflate})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	open := func(b []byte) *Archive {
		t.Helper()
		a, err := OpenArchive(bytes.NewReader(b), int64(len(b)))
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	good := buf.Bytes()
	if n, err := open(good).PeekShardRecordCount("a.pulse"); err != nil || n != 4 {
		t.Fatalf("deflated peek = %d, %v; want 4", n, err)
	}
	if _, err := open(good).PeekShardRecordCount("b.pulse"); !errors.HasCode(err, errors.PULSE_SHARD_MISSING) {
		t.Errorf("unknown entry: %v, want PULSE_SHARD_MISSING", err)
	}

	// Break the CRC-32 the central directory records (offset 16 of the
	// record); the inflated bytes no longer match it at EOF.
	bad := append([]byte(nil), good...)
	eocd := bad[len(bad)-zipEOCDLen:]
	dirOff := binary.LittleEndian.Uint32(eocd[16:])
	bad[dirOff+16] ^= 0xff
	if _, err := open(bad).PeekShardRecordCount("a.pulse"); !errors.HasCode(err, errors.ENCODING_IO) || !stderrors.Is(err, zip.ErrChecksum) {
		t.Errorf("deflated entry failing its CRC: %v, want ENCODING_IO wrapping zip.ErrChecksum", err)
	}
}

// hllTauReference is Ertl's tau evaluated term by term with math.Pow —
// an implementation independent of hllTau's square-root recurrence.
func hllTauReference(x float64) float64 {
	sum := 0.0
	for k := 1; k <= 60; k++ {
		d := 1 - math.Pow(x, math.Pow(2, -float64(k)))
		sum += d * d * math.Pow(2, -float64(k))
	}
	return (1 - x - sum) / 3
}

// TestIndexHLL_SaturatedRegisters: once registers reach the maximum
// rank, Ertl's tau term enters the estimate. tau matches its series
// definition, vanishes at 0 and 1, and the estimate grows with every
// further saturated register — a sketch with saturated registers never
// estimates below the same sketch with them merely high.
func TestIndexHLL_SaturatedRegisters(t *testing.T) {
	t.Parallel()

	if hllTau(0) != 0 || hllTau(1) != 0 {
		t.Errorf("tau(0), tau(1) = %v, %v; want 0, 0", hllTau(0), hllTau(1))
	}
	for _, x := range []float64{0.001, 0.1, 0.25, 0.5, 0.75, 0.999} {
		got, want := hllTau(x), hllTauReference(x)
		if math.Abs(got-want) > 1e-12 || got <= 0 {
			t.Errorf("tau(%v) = %v, want %v (> 0)", x, got, want)
		}
	}

	sketch := func(saturated int) *IndexHLL {
		h := NewIndexHLL()
		for i := range h.reg {
			h.reg[i] = 10
			if i < saturated {
				h.reg[i] = indexHLLMaxRank
			}
		}
		return h
	}
	prev := sketch(0).Estimate()
	for _, k := range []int{1, 64, 512, 1000} {
		est := sketch(k).Estimate()
		if est <= prev {
			t.Errorf("%d saturated registers: estimate %d, not above %d", k, est, prev)
		}
		prev = est
	}
	// The wire form carries a saturated register and decodes it.
	h, ok := DecodeIndexHLL(sketch(3).Encode())
	if !ok || h.Estimate() != sketch(3).Estimate() {
		t.Error("a sketch with saturated registers did not round-trip")
	}
}
