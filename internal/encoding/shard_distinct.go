package encoding

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// Per-shard distinct counts (U73 #500): the DIST trailer of a shard
// archive's `_schema.pulse` entry. Every archive writer (create, add —
// append and rewrite —, remove, compact, filter-to-file) records, per
// shard and per eligible field, the exact number of distinct stored
// words, so predict can bound a grouper's keys header-only. Single-file
// cohorts carry nothing: the trailer lives after the SHRD bytes of the
// `_schema.pulse` entry, never in a `.pulse` schema block, so no write
// moves the format version byte.
//
// Wire layout (little-endian), immediately after SHRD's shard_count:
//
//	magic        "DIST" (4)
//	version      u8 = DistinctTrailerVersion
//	body_length  u64 — the bytes that follow, so a reader may skip it
//	body:
//	  field_count u32
//	  field_count × { name_len u16, name, type u8 }
//	  shard_count u32
//	  shard_count × { name_len u16, name, field_count × { status u8, count u64 } }
//
// Distinctness is on the STORED WORD (the raw 4- or 8-byte bit
// pattern), with no canonicalisation: +0 and −0 count as two values, and
// every distinct NaN payload counts as its own value. The stat's
// consumer needs an UPPER bound on the keys any function of the stored
// word can mint, and collapsing either case could under-count it. No
// float arithmetic is involved, so the count is bit-identical on every
// architecture. Null rows are excluded (a null row mints no key; its
// stored word is ignored).

// DistinctTrailerMagic introduces the DIST trailer after SHRD.
var DistinctTrailerMagic = [4]byte{'D', 'I', 'S', 'T'}

// DistinctTrailerVersion is the DIST trailer's own version byte —
// independent of the `.pulse` format version.
const DistinctTrailerVersion byte = 1

// DefaultDistinctLimit is the default per-field, per-shard key cap
// (4,194,304 = 4 Mi keys): past it a shard's field is recorded
// DistinctOverLimit.
const DefaultDistinctLimit = 1 << 22

// DistinctStatus is the state of one (shard, field) count.
type DistinctStatus uint8

const (
	// DistinctAbsent: nothing is known — an archive written before the
	// trailer existed, a shard carried from one, or a field that became
	// eligible after the shard was counted. Never a fabricated count.
	DistinctAbsent DistinctStatus = 0
	// DistinctExact: Count is the exact number of distinct stored words.
	DistinctExact DistinctStatus = 1
	// DistinctOverLimit: the shard holds MORE than Count distinct
	// stored words (Count is the cap the writer used).
	DistinctOverLimit DistinctStatus = 2
	// DistinctOptedOut: the writer was told not to count
	// (pulse.Options.DisableShardDistinct). Count is 0.
	DistinctOptedOut DistinctStatus = 3
)

// String names the status for messages and tests.
func (s DistinctStatus) String() string {
	switch s {
	case DistinctAbsent:
		return "absent"
	case DistinctExact:
		return "exact"
	case DistinctOverLimit:
		return "over_limit"
	case DistinctOptedOut:
		return "opted_out"
	}
	return fmt.Sprintf("status(%d)", uint8(s))
}

// DistinctCount is one (shard, field) entry.
type DistinctCount struct {
	Status DistinctStatus
	Count  uint64
}

// DistinctField is one counted field: its logical name and type byte.
type DistinctField struct {
	Name string
	Type encoding.FieldType
}

// ShardDistinct is one shard's counts, parallel to ArchiveDistinct.Fields.
type ShardDistinct struct {
	Name   string
	Counts []DistinctCount
}

// ArchiveDistinct is the decoded DIST trailer: per-shard counts in
// archive (central-directory) order.
type ArchiveDistinct struct {
	Fields []DistinctField
	Shards []ShardDistinct
}

// DistinctEligible reports whether a field type is counted: the
// unbounded-ish integer and float words (u32, u64, date, datetime, f32,
// f64). Narrower words, dictionary-backed and set types already have a
// schema-known bound.
func DistinctEligible(ft encoding.FieldType) bool {
	switch ft {
	case encoding.FieldTypeU32, encoding.FieldTypeU64,
		encoding.FieldTypeDate, encoding.FieldTypeDateTime,
		encoding.FieldTypeF32, encoding.FieldTypeF64:
		return true
	}
	return false
}

// EligibleDistinctFields lists s's counted fields in logical order.
func EligibleDistinctFields(s *encoding.Schema) []DistinctField {
	var out []DistinctField
	for _, f := range s.Fields {
		if DistinctEligible(f.Type) {
			out = append(out, DistinctField{Name: f.Name, Type: f.Type})
		}
	}
	return out
}

// OptedOutCounts is a shard's entry when counting is disabled.
func OptedOutCounts(n int) []DistinctCount {
	out := make([]DistinctCount, n)
	for i := range out {
		out[i] = DistinctCount{Status: DistinctOptedOut}
	}
	return out
}

// Known reports whether the trailer holds a non-absent entry for shard
// on every one of fields (matched by name and type): what a carry would
// keep without leaving a gap. False on a nil trailer.
func (d *ArchiveDistinct) Known(shard string, fields []DistinctField) bool {
	if d == nil {
		return false
	}
	for _, sh := range d.Shards {
		if sh.Name != shard {
			continue
		}
		for _, f := range fields {
			fi := d.fieldIndex(f.Name)
			if fi < 0 || d.Fields[fi].Type != f.Type || sh.Counts[fi].Status == DistinctAbsent {
				return false
			}
		}
		return true
	}
	return false
}

// Lookup returns the (shard, field) entry; DistinctAbsent when either
// is not in the trailer.
func (d *ArchiveDistinct) Lookup(shard, field string) DistinctCount {
	if d == nil {
		return DistinctCount{}
	}
	fi := d.fieldIndex(field)
	if fi < 0 {
		return DistinctCount{}
	}
	for _, sh := range d.Shards {
		if sh.Name == shard {
			return sh.Counts[fi]
		}
	}
	return DistinctCount{}
}

// FieldBound is the archive-wide distinct bound for field: the sum of
// every shard's exact count (a sum over shards is a PROVEN upper bound
// on their union). ok is false — no bound — when the trailer is nil, the
// field is not tracked, the archive has no shards, or any shard's entry
// is not DistinctExact (absent, over the limit, opted out). The sum
// saturates at math.MaxUint64.
func (d *ArchiveDistinct) FieldBound(field string) (uint64, bool) {
	if d == nil || len(d.Shards) == 0 {
		return 0, false
	}
	fi := d.fieldIndex(field)
	if fi < 0 {
		return 0, false
	}
	var sum uint64
	for _, sh := range d.Shards {
		c := sh.Counts[fi]
		if c.Status != DistinctExact {
			return 0, false
		}
		if sum+c.Count < sum {
			sum = ^uint64(0)
			continue
		}
		sum += c.Count
	}
	return sum, true
}

func (d *ArchiveDistinct) fieldIndex(name string) int {
	for i, f := range d.Fields {
		if f.Name == name {
			return i
		}
	}
	return -1
}

// NextArchiveDistinct builds the trailer an archive about to be written
// carries: one entry per shard in names (archive order) over canonical's
// eligible fields. A shard in fresh takes those counts (aligned to
// EligibleDistinctFields(canonical)) — the shard was just counted. Any
// other shard CARRIES prev's entry, matched by shard name and field name
// AND type — its stored words did not change (remove, compact's carry,
// an add's stored shards; a set widen or group promotion re-lays-out
// records but never changes an eligible field's words). Whatever prev
// does not hold is DistinctAbsent: an archive written without the
// trailer never gets counts fabricated for its old shards.
func NextArchiveDistinct(canonical *encoding.Schema, names []string, prev *ArchiveDistinct, fresh map[string][]DistinctCount) *ArchiveDistinct {
	fields := EligibleDistinctFields(canonical)
	out := &ArchiveDistinct{Fields: fields, Shards: make([]ShardDistinct, len(names))}
	prevShard := map[string]int{}
	prevField := map[string]int{}
	if prev != nil {
		for i, sh := range prev.Shards {
			prevShard[sh.Name] = i
		}
		for i, f := range prev.Fields {
			prevField[f.Name] = i
		}
	}
	for i, name := range names {
		counts := make([]DistinctCount, len(fields))
		if fc, ok := fresh[name]; ok {
			copy(counts, fc)
		} else if si, ok := prevShard[name]; ok {
			for j, f := range fields {
				pf, ok := prevField[f.Name]
				if ok && prev.Fields[pf].Type == f.Type {
					counts[j] = prev.Shards[si].Counts[pf]
				}
			}
		}
		out.Shards[i] = ShardDistinct{Name: name, Counts: counts}
	}
	return out
}

// CountShardDistinct is a one-shot DistinctCounter.Count.
func CountShardDistinct(r io.Reader, canonical *encoding.Schema, fields []DistinctField, limit int) ([]DistinctCount, error) {
	return NewDistinctCounter(limit).Count(r, canonical, fields)
}

// DistinctCounter counts shard after shard of one archive write,
// REUSING its read buffer, record buffer and key sets between shards:
// a write over N shards allocates for its largest shard, never the sum.
// Not safe for concurrent use.
type DistinctCounter struct {
	limit int
	br    *bufio.Reader
	rec   []byte
	sets  []*distinctSet
}

// NewDistinctCounter returns a counter capping each field's keys at
// limit (<= 0: DefaultDistinctLimit).
func NewDistinctCounter(limit int) *DistinctCounter {
	if limit <= 0 {
		limit = DefaultDistinctLimit
	}
	return &DistinctCounter{limit: limit}
}

// Count reads one stored shard payload from r — its header, its own
// schema block (read past, layout-only), then its records — and counts
// each of fields' exact distinct stored words, decoding the records
// against canonical: the schema every reader decodes an archive's shards
// with (a grouped shard's row indices address the canonical group
// dictionaries). A field canonical does not carry, or carries with
// another type, is DistinctAbsent.
//
// Past the limit a field is DistinctOverLimit with Count = limit, and
// its set is released. Memory is one open-addressing set per counted
// field of at most limit/0.75 rounded up to a power of two uint64 slots
// (64 MiB at the default limit) — sized by the shard's distinct values,
// not its records. A truncated tail floors silently, as the record count
// does.
func (c *DistinctCounter) Count(r io.Reader, canonical *encoding.Schema, fields []DistinctField) ([]DistinctCount, error) {
	if c.br == nil {
		c.br = bufio.NewReaderSize(r, 64<<10)
	} else {
		c.br.Reset(r)
	}
	defer c.br.Reset(nil)
	br := c.br
	version, err := encoding.ReadHeader(br)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.PULSE_SHARD_HEADER_INVALID,
			"counting distinct values: reading shard header")
	}
	if _, err := ReadSchemaLayout(br, version); err != nil {
		return nil, errors.WrapCodedError(err, errors.PULSE_SHARD_HEADER_INVALID,
			"counting distinct values: reading shard schema")
	}
	logicalReader, logical, err := NewLogicalStream(br, canonical)
	if err != nil {
		return nil, err
	}

	type col struct {
		out, field, off, width int
		set                    *distinctSet
	}
	offsets := make([]int, len(logical.Fields))
	off := 0
	for i := range logical.Fields {
		offsets[i] = off
		off += logical.FieldWidth(i)
	}
	bitmapAt := off
	hasBitmap := logical.HasBitmap()

	counts := make([]DistinctCount, len(fields))
	var cols []col
	for j, f := range fields {
		for i, lf := range logical.Fields {
			if lf.Name != f.Name {
				continue
			}
			if lf.Type == f.Type && DistinctEligible(lf.Type) {
				cols = append(cols, col{out: j, field: i, off: offsets[i],
					width: logical.FieldWidth(i), set: c.set(len(cols))})
			}
			break
		}
	}
	stride := logical.RecordByteSize()
	if stride <= 0 {
		return counts, nil
	}
	if cap(c.rec) < stride {
		c.rec = make([]byte, stride)
	}
	rec := c.rec[:stride]
	for {
		if _, err := io.ReadFull(logicalReader, rec); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return nil, errors.WrapCodedError(err, errors.ENCODING_IO,
				"counting distinct values: reading shard records")
		}
		for k := range cols {
			cl := &cols[k]
			if cl.set.over {
				continue
			}
			if hasBitmap && encoding.BitmapIsNull(rec[bitmapAt:], cl.field) {
				continue
			}
			var key uint64
			if cl.width == 4 {
				key = uint64(binary.LittleEndian.Uint32(rec[cl.off:]))
			} else {
				key = binary.LittleEndian.Uint64(rec[cl.off:])
			}
			cl.set.add(key)
		}
	}
	for _, cl := range cols {
		if cl.set.over {
			counts[cl.out] = DistinctCount{Status: DistinctOverLimit, Count: uint64(c.limit)}
			continue
		}
		counts[cl.out] = DistinctCount{Status: DistinctExact, Count: uint64(cl.set.n)}
	}
	return counts, nil
}

// set returns the i-th reusable key set, emptied.
func (c *DistinctCounter) set(i int) *distinctSet {
	for len(c.sets) <= i {
		c.sets = append(c.sets, &distinctSet{})
	}
	s := c.sets[i]
	s.reset(c.limit)
	return s
}

// distinctSet is an exact open-addressing set of uint64 keys (linear
// probing, load ≤ 3/4) that stops — and releases its slots — the moment
// it would hold more than limit keys. Slot value 0 is "empty"; key 0 is
// tracked by hasZero. Hashing is an integer mix: bit-identical across
// architectures, and the count is exact whatever the hash.
type distinctSet struct {
	slots   []uint64
	hasZero bool
	n       int
	limit   int
	over    bool
}

// reset empties the set for reuse, keeping its slots' capacity.
func (s *distinctSet) reset(limit int) {
	clear(s.slots)
	s.hasZero = false
	s.n = 0
	s.limit = limit
	s.over = false
}

func (s *distinctSet) add(k uint64) {
	if k == 0 {
		if !s.hasZero {
			s.hasZero = true
			s.bump()
		}
		return
	}
	if len(s.slots) == 0 {
		s.slots = make([]uint64, 1024)
	} else if (s.n+1)*4 > len(s.slots)*3 {
		s.grow()
	}
	mask := uint64(len(s.slots) - 1)
	for i := mixKey(k) & mask; ; i = (i + 1) & mask {
		switch s.slots[i] {
		case k:
			return
		case 0:
			s.slots[i] = k
			s.bump()
			return
		}
	}
}

func (s *distinctSet) bump() {
	s.n++
	if s.n > s.limit {
		s.over = true
		s.slots = nil
	}
}

func (s *distinctSet) grow() {
	old := s.slots
	s.slots = make([]uint64, len(old)*2)
	mask := uint64(len(s.slots) - 1)
	for _, k := range old {
		if k == 0 {
			continue
		}
		i := mixKey(k) & mask
		for s.slots[i] != 0 {
			i = (i + 1) & mask
		}
		s.slots[i] = k
	}
}

// mixKey is the splitmix64 finaliser: integer-only.
func mixKey(k uint64) uint64 {
	k ^= k >> 30
	k *= 0xbf58476d1ce4e5b9
	k ^= k >> 27
	k *= 0x94d049bb133111eb
	k ^= k >> 31
	return k
}

// writeDistinctTrailer emits the DIST trailer for d.
func writeDistinctTrailer(w io.Writer, d *ArchiveDistinct) error {
	var body bytes.Buffer
	le := binary.LittleEndian
	_ = binary.Write(&body, le, uint32(len(d.Fields)))
	for _, f := range d.Fields {
		if err := writeShortName(&body, f.Name); err != nil {
			return err
		}
		body.WriteByte(byte(f.Type))
	}
	_ = binary.Write(&body, le, uint32(len(d.Shards)))
	for _, sh := range d.Shards {
		if len(sh.Counts) != len(d.Fields) {
			return errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
				"distinct trailer: a shard's counts do not match the field list",
				map[string]any{"shard": sh.Name, "counts": len(sh.Counts), "fields": len(d.Fields)})
		}
		if err := writeShortName(&body, sh.Name); err != nil {
			return err
		}
		for _, c := range sh.Counts {
			body.WriteByte(byte(c.Status))
			_ = binary.Write(&body, le, c.Count)
		}
	}
	var head [13]byte
	copy(head[:4], DistinctTrailerMagic[:])
	head[4] = DistinctTrailerVersion
	le.PutUint64(head[5:], uint64(body.Len()))
	if _, err := w.Write(head[:]); err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_IO, "writing schema-doc distinct trailer")
	}
	if _, err := w.Write(body.Bytes()); err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_IO, "writing schema-doc distinct trailer")
	}
	return nil
}

func writeShortName(w *bytes.Buffer, name string) error {
	if len(name) > 0xFFFF {
		return errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
			"distinct trailer: name longer than 65535 bytes",
			map[string]any{"length": len(name)})
	}
	_ = binary.Write(w, binary.LittleEndian, uint16(len(name)))
	w.WriteString(name)
	return nil
}

// readDistinctTrailer reads a DIST trailer from r, positioned right after
// SHRD. Nothing there, or another magic: (nil, nil). A trailer of a
// newer version: (nil, nil) — skipped, never misread. A truncated or
// inconsistent trailer: ENCODING_INVALID.
func readDistinctTrailer(r io.Reader) (*ArchiveDistinct, error) {
	var head [13]byte
	if _, err := io.ReadFull(r, head[:4]); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil, nil
		}
		return nil, distinctInvalid("reading magic", err)
	}
	if !bytes.Equal(head[:4], DistinctTrailerMagic[:]) {
		return nil, nil
	}
	if _, err := io.ReadFull(r, head[4:]); err != nil {
		return nil, distinctInvalid("truncated header", err)
	}
	if head[4] != DistinctTrailerVersion {
		return nil, nil
	}
	bodyLen := binary.LittleEndian.Uint64(head[5:])
	if bodyLen > 1<<40 {
		return nil, distinctInvalid("implausible body length", nil)
	}
	body, err := io.ReadAll(io.LimitReader(r, int64(bodyLen)))
	if err != nil {
		return nil, distinctInvalid("reading body", err)
	}
	if uint64(len(body)) != bodyLen {
		return nil, distinctInvalid("truncated body", nil)
	}
	p := &distinctParser{b: body}
	d := &ArchiveDistinct{}
	nf := p.u32()
	if uint64(nf)*3 > uint64(len(body)) {
		return nil, distinctInvalid("field count exceeds body", nil)
	}
	d.Fields = make([]DistinctField, 0, nf)
	for range nf {
		name := p.name()
		t := p.u8()
		d.Fields = append(d.Fields, DistinctField{Name: name, Type: encoding.FieldType(t)})
	}
	ns := p.u32()
	if uint64(ns)*2 > uint64(len(body)) {
		return nil, distinctInvalid("shard count exceeds body", nil)
	}
	d.Shards = make([]ShardDistinct, 0, ns)
	for range ns {
		sh := ShardDistinct{Name: p.name(), Counts: make([]DistinctCount, 0, nf)}
		for range nf {
			st := p.u8()
			c := p.u64()
			if st > uint8(DistinctOptedOut) {
				p.bad = true
			}
			sh.Counts = append(sh.Counts, DistinctCount{Status: DistinctStatus(st), Count: c})
			if p.bad {
				break
			}
		}
		d.Shards = append(d.Shards, sh)
		if p.bad {
			break
		}
	}
	if p.bad || p.i != len(p.b) {
		return nil, distinctInvalid("malformed body", nil)
	}
	return d, nil
}

func distinctInvalid(what string, cause error) error {
	msg := "schema-doc distinct trailer: " + what
	if cause != nil {
		return errors.WrapCodedError(cause, errors.ENCODING_INVALID, msg)
	}
	return errors.NewCodedError(errors.ENCODING_INVALID, msg)
}

type distinctParser struct {
	b   []byte
	i   int
	bad bool
}

func (p *distinctParser) take(n int) []byte {
	if p.bad || n < 0 || p.i+n > len(p.b) {
		p.bad = true
		return nil
	}
	out := p.b[p.i : p.i+n]
	p.i += n
	return out
}

func (p *distinctParser) u8() uint8 {
	if b := p.take(1); b != nil {
		return b[0]
	}
	return 0
}

func (p *distinctParser) u32() uint32 {
	if b := p.take(4); b != nil {
		return binary.LittleEndian.Uint32(b)
	}
	return 0
}

func (p *distinctParser) u64() uint64 {
	if b := p.take(8); b != nil {
		return binary.LittleEndian.Uint64(b)
	}
	return 0
}

func (p *distinctParser) name() string {
	b := p.take(2)
	if b == nil {
		return ""
	}
	return string(p.take(int(binary.LittleEndian.Uint16(b))))
}
