package cli

import (
	"bufio"
	"bytes"
	"io"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/returnshape"
	"github.com/frankbardon/pulse/types"
)

// writeEnvelopeStreamed writes env exactly as writeJSON(w, env) would —
// same bytes, same errEnvelopeFatal — without ever holding the whole
// document in memory.
//
// It exists because the --json envelope of a large process / chain /
// compose result was built by one json.Marshal: the compact body, then
// the indented copy, both as large as the output, on top of the result
// itself. Here the envelope is marshalled as a skeleton whose result
// rows (Response.Data — each chain stage's, each Compose slot's) are
// replaced by one empty sentinel row; the skeleton is then copied to w
// through a streaming indenter, and at each sentinel the real rows are
// marshalled and written one at a time, each through the same row
// encoder the --stream path uses (its `return` precision included).
//
// The engine still returns the whole result; only the encode streams.
// When the skeleton does not carry a sentinel where expected (a shape
// the splice does not recognise), the envelope is written whole by
// writeJSON, so the bytes never depend on the splice succeeding.
func writeEnvelopeStreamed(w io.Writer, env *descriptor.Envelope) error {
	if env == nil {
		return writeJSON(w, env)
	}
	skelData, targets := rowSkeleton(env.Data)
	if len(targets) == 0 {
		return writeJSON(w, env)
	}
	skelEnv := *env
	skelEnv.Data = skelData
	skel, err := types.MarshalFinite(&skelEnv)
	if err != nil {
		return err
	}
	// Dry run: every target with rows must resolve to the sentinel.
	matched, err := spliceRows(nil, skel, targets)
	if err != nil {
		return err
	}
	if matched != len(targets) {
		return writeJSON(w, env)
	}
	bw := bufio.NewWriterSize(w, streamChunk)
	ind := &jsonIndenter{w: bw}
	if _, err := spliceRows(ind, skel, targets); err != nil {
		return err
	}
	ind.emit('\n')
	if ind.err != nil {
		return ind.err
	}
	if err := bw.Flush(); err != nil {
		return err
	}
	if envelopeIsFatal(env) {
		return errEnvelopeFatal
	}
	return nil
}

// streamChunk is the buffer the streamed envelope is written through:
// the largest single Write it makes.
const streamChunk = 64 << 10

// sentinelRows stands in for a result's rows in the skeleton: one empty
// row, written `[{}]` — non-empty, so Response.Data's omitempty keeps
// the key where the real rows go.
var sentinelRows = []map[string]any{{}}

// rowTarget is one Response whose rows the splice writes, keyed in
// spliceRows by the JSON path of its `data` array.
type rowTarget struct {
	resp *types.Response
}

// rowSkeleton returns data with every row-bearing Response replaced by
// a shallow copy carrying sentinelRows, plus the splice targets keyed
// by the envelope path of each copy's `data` array. Pointer aliasing
// (ChainResponse.Final is the last stage) is preserved so the skeleton
// marshals the same structure. Non-result data yields no targets.
func rowSkeleton(data any) (any, map[string]rowTarget) {
	targets := map[string]rowTarget{}
	copies := map[*types.Response]*types.Response{}
	skel := func(r *types.Response, path string) *types.Response {
		if r == nil || len(r.Data) == 0 {
			return r
		}
		targets[path] = rowTarget{resp: r}
		if c, ok := copies[r]; ok {
			return c
		}
		c := *r
		c.Data = sentinelRows
		copies[r] = &c
		return &c
	}
	switch d := data.(type) {
	case *types.Response:
		if d == nil {
			return data, nil
		}
		return skel(d, "data/data"), targets
	case *types.ChainResponse:
		if d == nil {
			return data, nil
		}
		c := *d
		c.Stages = make([]*types.Response, len(d.Stages))
		for i, st := range d.Stages {
			c.Stages[i] = skel(st, "data/stages/"+strconv.Itoa(i)+"/data")
		}
		c.Final = skel(d.Final, "data/final/data")
		return &c, targets
	case *types.ComposedResponse:
		if d == nil {
			return data, nil
		}
		c := *d
		c.Responses = make([]*types.Response, len(d.Responses))
		for i, sub := range d.Responses {
			c.Responses[i] = skel(sub, "data/responses/"+strconv.Itoa(i)+"/data")
		}
		return &c, targets
	}
	return data, nil
}

// sentinelJSON is sentinelRows' compact form.
var sentinelJSON = []byte("[{}]")

// spliceFrame is one open object or array in the skeleton walk.
type spliceFrame struct {
	obj     bool
	wantKey bool
	key     string
	idx     int
}

// spliceRows walks the compact skeleton tracking the JSON path of each
// value. With ind nil it only counts the targets found holding the
// sentinel; otherwise it copies the skeleton into ind, writing each
// target's real rows in place of its sentinel. It returns the number
// of targets spliced.
func spliceRows(ind *jsonIndenter, skel []byte, targets map[string]rowTarget) (int, error) {
	var stack []spliceFrame
	matched := 0
	start := 0
	for i := 0; i < len(skel); {
		c := skel[i]
		switch c {
		case '"':
			j := i + 1
			for j < len(skel) && skel[j] != '"' {
				if skel[j] == '\\' {
					j++
				}
				j++
			}
			if n := len(stack); n > 0 && stack[n-1].wantKey {
				stack[n-1].key = string(skel[i+1 : j])
				stack[n-1].wantKey = false
			}
			i = j + 1
			continue
		case ',':
			if n := len(stack); n > 0 {
				if stack[n-1].obj {
					stack[n-1].wantKey = true
				} else {
					stack[n-1].idx++
				}
			}
		case '{':
			stack = append(stack, spliceFrame{obj: true, wantKey: true})
		case '}', ']':
			stack = stack[:len(stack)-1]
		case '[':
			t, ok := targets[splicePath(stack)]
			if ok && bytes.HasPrefix(skel[i:], sentinelJSON) {
				matched++
				if ind != nil {
					ind.write(skel[start:i])
					if err := writeRows(ind, t.resp); err != nil {
						return matched, err
					}
				}
				i += len(sentinelJSON)
				start = i
				continue
			}
			stack = append(stack, spliceFrame{})
		}
		i++
	}
	if ind != nil {
		ind.write(skel[start:])
	}
	return matched, nil
}

// splicePath renders the path of the value about to start inside the
// innermost open frame: object keys and array indexes joined by '/'.
func splicePath(stack []spliceFrame) string {
	var b strings.Builder
	for k, f := range stack {
		if k > 0 {
			b.WriteByte('/')
		}
		if f.obj {
			b.WriteString(f.key)
		} else {
			b.WriteString(strconv.Itoa(f.idx))
		}
	}
	return b.String()
}

// writeRows writes resp's rows as a JSON array, one row marshalled at a
// time through the row encoder Response.MarshalJSON applies in place
// (the slot's `return` precision; MarshalFinite when unshaped).
func writeRows(ind *jsonIndenter, resp *types.Response) error {
	ind.writeByte('[')
	for k, row := range resp.Data {
		if k > 0 {
			ind.writeByte(',')
		}
		b, err := returnshape.MarshalRow(resp, row)
		if err != nil {
			return err
		}
		ind.write(b)
	}
	ind.writeByte(']')
	return ind.err
}

// jsonIndenter streams JSON into w byte for byte as
// json.Encoder{SetIndent("", "  ")}.Encode(json.RawMessage(src)) would
// write src: insignificant whitespace dropped, '<' '>' '&' U+2028 and
// U+2029 escaped inside strings (the encoder's HTML-safe compaction),
// then two-space indentation with empty objects and arrays kept `{}` /
// `[]`. Input may arrive in any chunking. The first write error sticks.
type jsonIndenter struct {
	w          *bufio.Writer
	err        error
	depth      int
	needIndent bool
	inString   bool
	escaped    bool
	// pend holds the leading bytes of a possible U+2028 / U+2029
	// inside a string, split across chunks.
	pend []byte
}

func (j *jsonIndenter) write(src []byte) {
	for _, c := range src {
		j.step(c)
	}
}

func (j *jsonIndenter) writeByte(c byte) { j.step(c) }

func (j *jsonIndenter) emit(c byte) {
	if j.err == nil {
		j.err = j.w.WriteByte(c)
	}
}

func (j *jsonIndenter) emitString(s string) {
	if j.err == nil {
		_, j.err = j.w.WriteString(s)
	}
}

func (j *jsonIndenter) newline() {
	j.emit('\n')
	for range j.depth {
		j.emitString("  ")
	}
}

func (j *jsonIndenter) step(c byte) {
	if j.inString {
		j.stringByte(c)
		return
	}
	switch c {
	case ' ', '\t', '\n', '\r':
		return
	}
	if j.needIndent && c != '}' && c != ']' {
		j.needIndent = false
		j.depth++
		j.newline()
	}
	switch c {
	case '"':
		j.inString = true
		j.emit(c)
	case '{', '[':
		j.needIndent = true
		j.emit(c)
	case ',':
		j.emit(c)
		j.newline()
	case ':':
		j.emit(c)
		j.emit(' ')
	case '}', ']':
		if j.needIndent {
			j.needIndent = false
		} else {
			j.depth--
			j.newline()
		}
		j.emit(c)
	default:
		j.emit(c)
	}
}

// stringByte copies one byte inside a string literal, applying the
// encoder's HTML-safe escapes.
func (j *jsonIndenter) stringByte(c byte) {
	if len(j.pend) > 0 {
		j.pend = append(j.pend, c)
		if len(j.pend) == 2 && c == 0x80 {
			return
		}
		if len(j.pend) == 3 && (c == 0xA8 || c == 0xA9) {
			if c == 0xA8 {
				j.emitString("\\u2028")
			} else {
				j.emitString("\\u2029")
			}
			j.pend = j.pend[:0]
			return
		}
		// Not a line / paragraph separator: flush what was held, then
		// re-run the last byte from a clean state.
		held := j.pend[:len(j.pend)-1]
		for _, h := range held {
			j.emit(h)
		}
		j.pend = j.pend[:0]
		j.stringByte(c)
		return
	}
	if j.escaped {
		j.escaped = false
		j.emit(c)
		return
	}
	switch c {
	case '\\':
		j.escaped = true
		j.emit(c)
	case '"':
		j.inString = false
		j.emit(c)
	case '<':
		j.emitString("\\u003c")
	case '>':
		j.emitString("\\u003e")
	case '&':
		j.emitString("\\u0026")
	case 0xE2:
		j.pend = append(j.pend, c)
	default:
		j.emit(c)
	}
}
