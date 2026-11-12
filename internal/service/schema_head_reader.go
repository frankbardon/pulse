package service

import "io"

// Chunk bounds of headReader. The first fill is small so a tiny cohort
// is not drained by Open; each later fill is sized to the bytes the
// parser has consumed so far (never under the floor, never over the
// ceiling).
const (
	headReaderMinChunk = 64
	headReaderMaxChunk = 1 << 20
	// headReaderInline is the buffer carried inside the reader itself,
	// so a typical small schema decodes with no buffer allocation.
	headReaderInline = 512
)

// headReader is the reader Service.Open decodes a single file's header
// + schema through. The schema block carries no total length (a 0x01
// block has no prefix at all, and a 0x02 extension length does not
// cover the inline dictionaries), so the read cannot be sized up front;
// decoding it straight off the file handle cost two reads per inline
// dictionary entry (~0.45 s at 600k entries).
//
// Instead the buffer fills in chunks that grow with what the parser has
// consumed: max(headReaderMinChunk, consumed), capped at
// headReaderMaxChunk. The over-read past the schema end is therefore
// bounded by one chunk — at most max(64 bytes, the header + schema
// length), and never more than 1 MiB — while the read count is
// logarithmic in the schema size up to the cap and linear in MiB past
// it. exact turns the buffer off (every read goes straight to the
// handle) for callers that must not read one byte past the schema.
//
// The reader starts holding prefix — the magic bytes Open already read
// to dispatch — and consumed counts every byte handed to the parser
// from the file's first byte, NOT the bytes fetched from the file: it
// is the payload offset, which the handle's position (one chunk ahead)
// no longer is.
type headReader struct {
	r        io.Reader
	exact    bool
	buf      []byte
	off, end int
	consumed int64
	inline   [headReaderInline]byte
}

func newHeadReader(r io.Reader, prefix []byte, exact bool) *headReader {
	h := &headReader{r: r, exact: exact}
	h.buf = h.inline[:]
	h.end = copy(h.buf, prefix)
	return h
}

func (h *headReader) nextChunk() int {
	n := h.consumed
	if n < headReaderMinChunk {
		n = headReaderMinChunk
	}
	if n > headReaderMaxChunk {
		n = headReaderMaxChunk
	}
	return int(n)
}

func (h *headReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if h.off == h.end {
		chunk := h.nextChunk()
		if h.exact || len(p) >= chunk {
			// Straight through: no copy, and no over-read beyond what
			// the parser asked for.
			n, err := h.r.Read(p)
			h.consumed += int64(n)
			return n, err
		}
		if cap(h.buf) < chunk {
			h.buf = make([]byte, chunk)
		}
		n, err := h.r.Read(h.buf[:chunk])
		h.off, h.end = 0, n
		if n == 0 {
			return 0, err
		}
		// A fill that also reported an error surfaces it on the next
		// empty-buffer Read, as bufio does.
	}
	n := copy(p, h.buf[h.off:h.end])
	h.off += n
	h.consumed += int64(n)
	return n, nil
}
