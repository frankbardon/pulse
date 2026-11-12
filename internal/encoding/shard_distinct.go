package encoding

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"hash"
	"hash/crc32"
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
//	  shard_count × { name_len u16, name, identity, field_count × { status u8, count u64 } }
//	identity (version 2 only):
//	  known u8 (0 / 1), size u64, crc32 u32 — the stored payload's
//	  uncompressed size and CRC-32, the values its central-directory
//	  record carries; zero when known = 0
//
// Version 1 (no identity) is still read: its shards' identities are
// unknown, so their counts are never trusted for a carry or a bound
// (ShardIdentity, Stale).
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
// independent of the `.pulse` format version. Version 2 added the
// per-shard identity (U73 #720); version 1 still reads, identity
// unknown.
const DistinctTrailerVersion byte = 2

// distinctTrailerV1 is the identity-less layout every writer emitted
// before per-shard identities.
const distinctTrailerV1 byte = 1

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

// ShardIdentity names the exact bytes a shard's counts were taken
// from: the stored payload's uncompressed size and CRC-32 — the values
// the shard's zip central-directory record carries (EntryIdentity), the
// identity an archive point-lookup index keys a shard by too. A count is
// only ever trusted for bytes of the same identity: a shard entry
// replaced outside Pulse under the same name moves its CRC-32. Known is
// false when the writer never saw the bytes (an opted-out create) or the
// trailer predates identities (version 1).
type ShardIdentity struct {
	Known bool
	Size  uint64
	CRC32 uint32
}

// EntryIdentity is the identity of an archive entry, read from its
// central-directory record (never recomputed from the payload).
func EntryIdentity(e ArchiveEntry) ShardIdentity {
	return ShardIdentity{Known: true, Size: uint64(e.Size), CRC32: e.CRC32}
}

// PayloadIdentity is the identity an archive storing payload records
// for it.
func PayloadIdentity(payload []byte) ShardIdentity {
	return ShardIdentity{Known: true, Size: uint64(len(payload)), CRC32: crc32.ChecksumIEEE(payload)}
}

// IdentityHasher accumulates a payload's identity as it is written
// through (an io.TeeReader's sink), so a writer that streams a shard
// learns its identity in the same read.
type IdentityHasher struct {
	h hash.Hash32
	n uint64
}

// NewIdentityHasher returns an empty hasher.
func NewIdentityHasher() *IdentityHasher {
	return &IdentityHasher{h: crc32.NewIEEE()}
}

// Write hashes p.
func (h *IdentityHasher) Write(p []byte) (int, error) {
	h.n += uint64(len(p))
	return h.h.Write(p)
}

// Identity is the identity of every byte written so far.
func (h *IdentityHasher) Identity() ShardIdentity {
	return ShardIdentity{Known: true, Size: h.n, CRC32: h.h.Sum32()}
}

// vouches reports whether a recorded identity proves its counts
// describe bytes of identity cur: both known and equal.
func (id ShardIdentity) vouches(cur ShardIdentity) bool {
	return id.Known && cur.Known && id == cur
}

// ShardDistinct is one shard's counts, parallel to ArchiveDistinct.Fields,
// and the identity of the bytes they were taken from.
type ShardDistinct struct {
	Name     string
	Identity ShardIdentity
	Counts   []DistinctCount
}

// claims reports whether any of the shard's counts says something about
// its bytes — exact, or over the limit. An absent or opted-out entry
// claims nothing, so it can never be stale.
func (s *ShardDistinct) claims() bool {
	for _, c := range s.Counts {
		if c.Status == DistinctExact || c.Status == DistinctOverLimit {
			return true
		}
	}
	return false
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
// on every one of fields (matched by name and type) that is not Stale at
// cur, the identity of the bytes the shard holds now: what a carry would
// keep without leaving a gap. False on a nil trailer.
func (d *ArchiveDistinct) Known(shard string, cur ShardIdentity, fields []DistinctField) bool {
	sh := d.shard(shard)
	if sh == nil || d.Stale(shard, cur) {
		return false
	}
	for _, f := range fields {
		fi := d.fieldIndex(f.Name)
		if fi < 0 || d.Fields[fi].Type != f.Type || sh.Counts[fi].Status == DistinctAbsent {
			return false
		}
	}
	return true
}

// Stale reports whether the trailer holds an entry for shard whose
// counts claim something (exact, over the limit) that its recorded
// identity cannot vouch for at cur — the identity of the bytes the shard
// holds now: the identity was never recorded (a version-1 trailer) or
// differs (the entry was replaced outside Pulse under the same name). A
// writer RECOUNTS a stale shard; NextArchiveDistinct never carries one.
// An absent or opted-out entry claims nothing and is never stale. False
// on a nil trailer or an unknown shard.
func (d *ArchiveDistinct) Stale(shard string, cur ShardIdentity) bool {
	sh := d.shard(shard)
	return sh != nil && sh.claims() && !sh.Identity.vouches(cur)
}

// VouchedBy reports whether every shard entry is vouched for by the
// identity cur returns for its name (false = no such entry): no entry
// claims a count its recorded identity cannot prove describes the
// shard's current bytes. The bound readers (predict, the run's
// pre-flight) take counts only from a trailer it accepts, so a shard
// replaced outside Pulse never lends its old counts to a bound.
func (d *ArchiveDistinct) VouchedBy(cur func(shard string) (ShardIdentity, bool)) bool {
	if d == nil {
		return false
	}
	for i := range d.Shards {
		id, ok := cur(d.Shards[i].Name)
		if !ok || (d.Shards[i].claims() && !d.Shards[i].Identity.vouches(id)) {
			return false
		}
	}
	return true
}

func (d *ArchiveDistinct) shard(name string) *ShardDistinct {
	if d == nil {
		return nil
	}
	for i := range d.Shards {
		if d.Shards[i].Name == name {
			return &d.Shards[i]
		}
	}
	return nil
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

// DistinctShardRef is one shard of an archive about to be written, as
// NextArchiveDistinct sees it.
type DistinctShardRef struct {
	Name string
	// Current is the identity of the bytes the shard holds in the archive
	// being rewritten — what prev's entry must be vouched for at before
	// it carries. Unused for a shard counted fresh.
	Current ShardIdentity
	// Stored is the identity of the bytes the written archive stores for
	// the shard — recorded in the new trailer. It differs from Current
	// only when the write re-encodes the shard (an add's archive widen or
	// group promotion).
	Stored ShardIdentity
}

// KeptShard is the ref of a shard whose bytes the write keeps (a raw
// copy): Current and Stored are both id.
func KeptShard(name string, id ShardIdentity) DistinctShardRef {
	return DistinctShardRef{Name: name, Current: id, Stored: id}
}

// NextArchiveDistinct builds the trailer an archive about to be written
// carries: one entry per shard in shards (archive order) over
// canonical's eligible fields, each stamped with its Stored identity. A
// shard in fresh takes those counts (aligned to
// EligibleDistinctFields(canonical)) — the shard was just counted. Any
// other shard CARRIES prev's entry, matched by shard name and field name
// AND type — its stored words did not change (remove, compact's carry,
// an add's stored shards; a set widen or group promotion re-lays-out
// records but never changes an eligible field's words) — unless that
// entry is Stale at the shard's Current identity (the entry was replaced
// outside Pulse, or the trailer predates identities): a stale entry
// never carries, and a shard the caller did not recount is then
// DistinctAbsent. Whatever prev does not hold is DistinctAbsent too: an
// archive written without the trailer never gets counts fabricated for
// its old shards.
func NextArchiveDistinct(canonical *encoding.Schema, shards []DistinctShardRef, prev *ArchiveDistinct, fresh map[string][]DistinctCount) *ArchiveDistinct {
	fields := EligibleDistinctFields(canonical)
	out := &ArchiveDistinct{Fields: fields, Shards: make([]ShardDistinct, len(shards))}
	prevField := map[string]int{}
	if prev != nil {
		for i, f := range prev.Fields {
			prevField[f.Name] = i
		}
	}
	for i, ref := range shards {
		counts := make([]DistinctCount, len(fields))
		if fc, ok := fresh[ref.Name]; ok {
			copy(counts, fc)
		} else if ps := prev.shard(ref.Name); ps != nil && !prev.Stale(ref.Name, ref.Current) {
			for j, f := range fields {
				pf, ok := prevField[f.Name]
				if ok && prev.Fields[pf].Type == f.Type {
					counts[j] = ps.Counts[pf]
				}
			}
		}
		out.Shards[i] = ShardDistinct{Name: ref.Name, Identity: ref.Stored, Counts: counts}
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

// writeDistinctTrailer emits the DIST trailer for d at
// DistinctTrailerVersion.
func writeDistinctTrailer(w io.Writer, d *ArchiveDistinct) error {
	return writeDistinctTrailerVersion(w, d, DistinctTrailerVersion)
}

// writeDistinctTrailerVersion emits d in the given trailer layout
// (version 1 drops the identities — kept for the compatibility tests).
func writeDistinctTrailerVersion(w io.Writer, d *ArchiveDistinct, version byte) error {
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
		if version >= DistinctTrailerVersion {
			// An unknown identity writes all zeros: its size and CRC
			// carry nothing.
			var id ShardIdentity
			var known byte
			if sh.Identity.Known {
				id, known = sh.Identity, 1
			}
			body.WriteByte(known)
			_ = binary.Write(&body, le, id.Size)
			_ = binary.Write(&body, le, id.CRC32)
		}
		for _, c := range sh.Counts {
			body.WriteByte(byte(c.Status))
			_ = binary.Write(&body, le, c.Count)
		}
	}
	var head [13]byte
	copy(head[:4], DistinctTrailerMagic[:])
	head[4] = version
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
// newer version: (nil, nil) — skipped, never misread. A version-1
// trailer reads with every shard's identity unknown. A truncated or
// inconsistent trailer (including an identity flag other than 0 / 1):
// ENCODING_INVALID.
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
	version := head[4]
	if version != DistinctTrailerVersion && version != distinctTrailerV1 {
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
		if version >= DistinctTrailerVersion {
			known := p.u8()
			size, crc := p.u64(), p.u32()
			switch known {
			case 0:
			case 1:
				sh.Identity = ShardIdentity{Known: true, Size: size, CRC32: crc}
			default:
				p.bad = true
			}
		}
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
