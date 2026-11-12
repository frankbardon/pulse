package encoding

import (
	"io"
	"sync"
)

// Fill bounds of schemaReader: the first fill is small, each later one
// sized to the bytes consumed so far, never over the ceiling — and
// never past the bytes the block is PROVEN to still hold (limit).
const (
	schemaReaderMinChunk = 64
	schemaReaderMaxChunk = 1 << 20
	// schemaReaderLayoutChunk caps the fills of a layout-only read,
	// whose point is to read past the dictionaries without allocating
	// for them: its buffer stays small, at more reads.
	schemaReaderLayoutChunk = 16 << 10
	// schemaReaderInline is the buffer carried inside the reader, so a
	// typical small schema decodes with no further buffer allocation.
	schemaReaderInline = 512
	// minFieldDescriptorBytes is the smallest field descriptor on the
	// wire: type, nullable, name length, byte offset, bit position, csv
	// column index, description length — with an empty name and
	// description and no decimal metadata or dictionary.
	minFieldDescriptorBytes = 1 + 1 + 2 + 4 + 1 + 2 + 2
)

// schemaReader buffers a schema-block decode without ever reading a
// byte past the block's end — the first record byte (archive-storage
// E5-S4, TODO #722). The block carries no total length (a 0x01 block has
// no prefix at all, and a 0x02 extension length does not cover the
// inline dictionaries), so decoding it straight off a file handle cost
// one read per primitive: two per inline dictionary entry, ~1.2M reads
// (~0.45 s) at 600k entries.
//
// Instead the decoder reports, as it learns them, lower bounds on the
// bytes the block still holds (ahead): field_count × the smallest
// descriptor, a dictionary's remaining entries × 2 (each carries at
// least its u16 length) plus the label in hand, a 0x02 extension
// block's declared length. A fill fetches at most the bytes so proven,
// so on a well-formed block the source is never read at or past the
// first record byte and is left EXACTLY there once the decode returns:
// every proven byte was decoded, so the buffer is empty. When nothing
// is proven ahead a read goes straight to the source for exactly the
// bytes the decoder asked for — the unbuffered behaviour. Only a
// corrupt block (a count larger than its entries) can draw a fill past
// its end, and its decode then fails.
//
// The read count is logarithmic in the block size: the proven bound
// over a dictionary's tail shrinks geometrically.
type schemaReader struct {
	r        io.Reader
	buf      []byte
	off, end int
	// consumed counts bytes handed to the decoder; limit is the
	// consumed-relative offset the block is proven to reach.
	consumed, limit int64
	maxChunk        int64
	inline          [schemaReaderInline]byte
}

// schemaReaders recycles schemaReader values, so a schema decode adds
// no allocation to the operations whose allocation counts are pinned
// (TestObservabilityOffNoAllocs). Only the inline buffer is recycled; a
// grown one is dropped with its decode.
var schemaReaders = sync.Pool{New: func() any { return new(schemaReader) }}

// newSchemaReader wraps r for one schema-block decode, its fills capped
// at maxChunk; hand the result to releaseSchemaReader once the decode
// returns. A source that already serves small reads cheaply — an
// in-memory or bufio reader, recognised by io.ByteReader — is returned
// as is, never double buffered.
func newSchemaReader(r io.Reader, maxChunk int64) io.Reader {
	if _, ok := r.(io.ByteReader); ok {
		return r
	}
	sr := schemaReaders.Get().(*schemaReader)
	sr.r, sr.maxChunk = r, maxChunk
	sr.buf = sr.inline[:]
	return sr
}

// releaseSchemaReader returns r to the pool when newSchemaReader made
// it; r must not be read again.
func releaseSchemaReader(r io.Reader) {
	sr, ok := r.(*schemaReader)
	if !ok {
		return
	}
	*sr = schemaReader{}
	schemaReaders.Put(sr)
}

// schemaAhead reports to r, when it is a schemaReader, that at least n
// more bytes of the schema block follow what the decoder has consumed.
func schemaAhead(r io.Reader, n int64) {
	if sr, ok := r.(*schemaReader); ok {
		sr.ahead(n)
	}
}

func (s *schemaReader) ahead(n int64) {
	if e := s.consumed + n; e > s.limit {
		s.limit = e
	}
}

func (s *schemaReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if s.off == s.end {
		// The buffer is drained, so the source sits at consumed.
		chunk := min(max(s.consumed, schemaReaderMinChunk), s.maxChunk, s.limit-s.consumed)
		if chunk <= int64(len(p)) {
			// Nothing (or too little) proven ahead: straight through,
			// exactly the bytes the decoder asked for.
			n, err := s.r.Read(p)
			s.consumed += int64(n)
			return n, err
		}
		if int64(cap(s.buf)) < chunk {
			s.buf = make([]byte, chunk)
		}
		n, err := s.r.Read(s.buf[:chunk])
		s.off, s.end = 0, n
		if n == 0 {
			return 0, err
		}
		// A fill that also reported an error surfaces it on the next
		// empty-buffer Read, as bufio does.
	}
	n := copy(p, s.buf[s.off:s.end])
	s.off += n
	s.consumed += int64(n)
	return n, nil
}
