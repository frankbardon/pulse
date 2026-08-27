package spss

// SPSS bytecode compression: the command table, the bias arithmetic, and the
// decoder.
//
// This is the encoding SPSS writes BY DEFAULT, so it is the encoding almost
// every real `.sav` file in the world uses. It is lossless — a bytecode
// stream and an uncompressed data section can carry byte-identical case data
// — and the saving comes from the observation that survey data is mostly
// small whole numbers, which fit in one byte instead of eight.
//
// # The stream
//
// The data section is a sequence of BLOCKS. Each block is eight command
// bytes followed, immediately, by the eight-byte payloads that those commands
// asked for, in command order. So a block is 8 bytes long when no command in
// it needs a payload and 72 bytes long when all eight do. The commands are:
//
//	0        pad — occupies a command slot and produces no element at all.
//	         Writers use it to fill out the final block.
//	1..251   the integer `command - bias`, where bias comes from the file
//	         header. With the conventional bias of 100 that is every whole
//	         number from -99 to 151.
//	252      end of file. Nothing after it in the block is read.
//	253      the next unread eight bytes of the stream are the element,
//	         verbatim — the escape hatch for everything the other commands
//	         cannot say.
//	254      an all-spaces eight-byte string segment.
//	255      the system-missing sentinel.
//
// Every command except 0 and 252 produces exactly one 8-byte element, and
// elements run on in the same order an uncompressed section would have them:
// case after case, and within a case, element after element in dictionary
// order. There is no per-case framing here either — the case stride is still
// the only thing that says where one case ends.
//
// # Why the table is in its own file
//
// The bytecode ENCODER is this decoder's exact mirror, and an encoder that
// disagrees with its decoder by one about the compressible range, or that
// rounds the bias differently, is a silent data-corruption bug that
// round-tripping through our own codec would not necessarily catch — both
// halves would share the mistake and agree. So the command names, the
// compressible range and the bias arithmetic live here, once, and both
// directions are expressed in terms of them: [commandValue] decodes and
// [valueCommand] encodes, and they are each other's inverse by construction.
// Nothing outside this file may spell 252, 253, 254 or 255.
//
// # Strictness
//
// A command is checked against the kind of element the dictionary says
// occupies that position: a numeric element cannot be an all-spaces string
// segment (254) and a string element has no system-missing state (255), so
// either combination means the stream has desynchronised from the
// dictionary. Accepting them would put 0x2020202020202020 into a numeric
// column as a plausible 1.5e-153, which is exactly the plausible-looking
// garbage the reader refuses to produce elsewhere. The check matches PSPP,
// which errors on both.

import (
	"math"
	"strconv"

	"github.com/frankbardon/pulse/errors"
)

// Command byte values. The names, not the numbers, are what the rest of the
// package uses.
const (
	// cmdPad occupies a command slot without producing an element. It is
	// how a writer fills out the last block of the stream.
	cmdPad byte = 0

	// cmdIntMin and cmdIntMax bound the range of commands that encode a
	// whole number as `command - bias`. They are a closed interval.
	cmdIntMin byte = 1
	cmdIntMax byte = 251

	// cmdEOF ends the data section.
	cmdEOF byte = 252

	// cmdRaw says the next unread eight bytes of the stream are the
	// element verbatim.
	cmdRaw byte = 253

	// cmdSpaces is an all-spaces eight-byte string segment.
	cmdSpaces byte = 254

	// cmdSysmis is the system-missing sentinel.
	cmdSysmis byte = 255
)

// commandBlockSize is the number of command bytes that arrive together
// before their payloads. It is what makes the stream self-framing.
const commandBlockSize = 8

// spacesElement is the element cmdSpaces stands for.
var spacesElement = [elementSize]byte{' ', ' ', ' ', ' ', ' ', ' ', ' ', ' '}

// commandValue decodes an integer command under a bias.
//
// It is deliberately total: every byte in [cmdIntMin, cmdIntMax] has a value,
// and the caller has already established that the command is in that range.
func commandValue(cmd byte, bias float64) float64 {
	return float64(cmd) - bias
}

// valueCommand encodes v as an integer command under a bias, reporting
// whether v is encodable that way at all. A false return means the value
// needs cmdRaw.
//
// This is [commandValue]'s exact inverse, and the assertion that it is one is
// the point of the function existing before there is an encoder to call it:
// commandValue(c, bias) == v for every (c, ok) it returns true for. The
// integrality of the SUM is checked, not just of v, because a fractional bias
// would otherwise let a whole number encode into a command byte that decodes
// back to a different number — the rounding disagreement that makes an
// encoder and a decoder silently disagree.
func valueCommand(v, bias float64) (byte, bool) {
	if v != math.Trunc(v) {
		// Catches NaN too: Trunc(NaN) is NaN and NaN != NaN.
		return 0, false
	}
	sum := v + bias
	if sum != math.Trunc(sum) || math.IsInf(sum, 0) {
		return 0, false
	}
	if sum < float64(cmdIntMin) || sum > float64(cmdIntMax) {
		return 0, false
	}
	return byte(sum), true
}

// usableBias reports whether a header's compression bias can be used as
// arithmetic. A non-finite bias would turn every integer command into NaN,
// which is data this reader has no way to justify emitting.
func usableBias(bias float64) bool {
	return !math.IsNaN(bias) && !math.IsInf(bias, 0)
}

// ---------------------------------------------------------------------------
// Element kinds
// ---------------------------------------------------------------------------

// elementKind says what the dictionary declares occupies one 8-byte element
// position within a case. It is what makes the command check possible: a
// command is only legal for some kinds.
type elementKind uint8

const (
	// elemUnclaimed is the zero value: no variable in the dictionary
	// covers this element. A well-formed file has none — every element is
	// claimed either by a variable or by one of its string continuation
	// records — so nothing is judged here, and any command is accepted.
	elemUnclaimed elementKind = iota

	// elemNumeric is a numeric variable's single element.
	elemNumeric

	// elemString is one 8-byte segment of a string variable.
	elemString
)

// allows reports whether a command may produce an element of this kind.
func (k elementKind) allows(cmd byte) bool {
	switch cmd {
	case cmdSpaces:
		return k != elemNumeric
	case cmdSysmis:
		return k != elemString
	case cmdRaw:
		return true
	default:
		// An integer command. A string element has no numeric form.
		return k != elemString
	}
}

// commandDescription names a command in a diagnostic, so the message says
// what the stream asked for rather than only which byte it was.
func commandDescription(cmd byte) string {
	switch cmd {
	case cmdSpaces:
		return "an all-spaces string segment (command 254)"
	case cmdSysmis:
		return "the system-missing sentinel (command 255)"
	case cmdRaw:
		return "a verbatim 8-byte value (command 253)"
	case cmdEOF:
		return "end of file (command 252)"
	case cmdPad:
		return "padding (command 0)"
	default:
		return "an integer literal (command " + strconv.Itoa(int(cmd)) + ")"
	}
}

// description names an element kind in a diagnostic.
func (k elementKind) description() string {
	switch k {
	case elemNumeric:
		return "a numeric element"
	case elemString:
		return "a string segment"
	case elemUnclaimed:
		return "an element no variable claims"
	default:
		return "an element of an unknown kind"
	}
}

// ---------------------------------------------------------------------------
// The command stream
// ---------------------------------------------------------------------------

// streamLocator maps an index into a command stream onto the byte offset a
// diagnostic should name in the file the user actually holds.
//
// It exists because the command stream is not always a window onto the file.
// For a bytecode-compressed `.sav` it is: index i sits at file offset
// dataOffset+i, and [fileLocator] says exactly that. For a ZSAV the stream is
// the CONCATENATION of the inflated zlib blocks, so an index into it has no
// file offset of its own — the nearest honest answer is where the block
// holding it starts on disk, which is what zsavIndex.locator returns.
type streamLocator func(i int) int

// fileLocator is the identity mapping for a stream that IS a window onto the
// file, starting at base.
func fileLocator(base int) streamLocator {
	return func(i int) int { return base + i }
}

// bytecodeStream reads commands and their payloads out of a data section.
//
// The interleaving is the whole subtlety: pos points at the next unread byte,
// which after a block of commands is loaded points at that block's PAYLOAD
// region. So loading the next block and reading a payload both advance the
// same cursor, and a payload read that happens between two commands of one
// block correctly consumes bytes that sit after the whole block.
type bytecodeStream struct {
	// src is the command stream, from its first byte.
	src []byte

	// loc turns an index into src into the file offset a diagnostic
	// names. See streamLocator for why it is a function rather than a
	// base offset.
	loc streamLocator

	// pos is the next unread byte of src.
	pos int

	// block is the current eight command bytes, idx the next unread one,
	// and blockPos the position block was loaded from. idx starts spent so
	// the first next() loads.
	block    [commandBlockSize]byte
	idx      int
	blockPos int
}

func newBytecodeStream(src []byte, loc streamLocator) *bytecodeStream {
	return &bytecodeStream{src: src, loc: loc, idx: commandBlockSize}
}

// offset converts an index into src into a file offset.
func (s *bytecodeStream) offset(i int) int { return s.loc(i) }

// next returns the next command that produces something, its file offset, and
// whether there was one. Padding is consumed and skipped here rather than
// surfaced, which is exactly what "0 = ignore" means.
//
// ok is false when the stream runs out of command bytes. That is not
// necessarily an error — a writer may end the stream by simply stopping —
// so the decision is the caller's.
func (s *bytecodeStream) next() (cmd byte, off int, ok bool) {
	for {
		if s.idx >= commandBlockSize {
			if s.pos+commandBlockSize > len(s.src) {
				return 0, s.offset(s.pos), false
			}
			copy(s.block[:], s.src[s.pos:s.pos+commandBlockSize])
			s.blockPos = s.pos
			s.pos += commandBlockSize
			s.idx = 0
		}
		c := s.block[s.idx]
		o := s.offset(s.blockPos + s.idx)
		s.idx++
		if c == cmdPad {
			continue
		}
		return c, o, true
	}
}

// payload reads the eight-byte value a cmdRaw command names.
//
// The returned slice aliases src. Callers copy it into the output buffer
// immediately and never retain it.
func (s *bytecodeStream) payload() (b []byte, off int, ok bool) {
	off = s.offset(s.pos)
	if s.pos+elementSize > len(s.src) {
		return nil, off, false
	}
	b = s.src[s.pos : s.pos+elementSize]
	s.pos += elementSize
	return b, off, true
}

// ---------------------------------------------------------------------------
// The decoder
// ---------------------------------------------------------------------------

// decodeBytecode expands a bytecode-compressed data section into the flat
// uncompressed case bytes the rest of the package reads.
//
// Materialising the whole section rather than decoding lazily is deliberate.
// The schema mapping already walks every case — the categorical widths, the
// declared nullability and the temporal rules are all statements about the
// whole column — so the section is read in full regardless, and expanding
// once means ReadRows, a second ReadRows after a Reset, and the mapping scan
// all address cases by the same fixed stride that an uncompressed file gives
// them for free. The alternative, a stream that must be replayed from the
// start to reach case n, would make the case geometry conditional on the
// compression flag in five places instead of none.
//
// It returns the expanded bytes and the number of whole cases in them.
func decodeBytecode(d *dictionary, data []byte, plan *dataPlan) ([]byte, int, error) {
	if d.dataOffset > len(data) {
		// Unreachable for a parsed dictionary; guarded because the
		// slice below would panic rather than fail.
		return nil, 0, dataError(errors.PULSE_SPSS_DATA_TRUNCATED, len(data),
			"the dictionary ends at byte offset %d but the file is only %d byte(s) long",
			d.dataOffset, len(data))
	}
	return decodeBytecodeStream(d, plan, data[d.dataOffset:], fileLocator(d.dataOffset))
}

// decodeBytecodeStream is the decoder proper: it expands one command stream,
// wherever that stream came from.
//
// It is separate from [decodeBytecode] because a ZSAV data section is a
// bytecode stream too — the zlib blocks inflate TO one, they do not replace
// it — so ZSAV decoding is "inflate, then run this". src is the stream's
// bytes and loc says how to name a position in them; everything else about
// the expansion is identical, which is the point.
func decodeBytecodeStream(d *dictionary, plan *dataPlan, src []byte, loc streamLocator) ([]byte, int, error) {
	bias := d.header.bias
	if !usableBias(bias) {
		return nil, 0, dataError(errors.PULSE_SPSS_COMPRESSION_INVALID, d.dataOffset,
			"the header declares a compression bias of %v, which is not a usable number; every integer command in the stream would decode to NaN",
			bias)
	}

	st := newBytecodeStream(src, loc)
	kinds := plan.elemKinds

	var sysmis [elementSize]byte
	plan.bo.PutUint64(sysmis[:], plan.sysmisBits)

	out := make([]byte, 0, decodedSizeHint(d, plan, len(st.src)))

	elem := 0 // element position within the case being assembled
	for {
		cmd, off, ok := st.next()
		if !ok {
			// The command bytes ran out. A stream that stopped on a
			// case boundary is accepted — the check below is what
			// decides — because a writer ending without cmdEOF is
			// sloppy rather than corrupt.
			break
		}
		if cmd == cmdEOF {
			break
		}

		kind := kinds[elem]
		if !kind.allows(cmd) {
			return nil, 0, dataError(errors.PULSE_SPSS_COMPRESSION_INVALID, off,
				"the compressed stream asks for %s at element %d of a case, where the dictionary declares %s; the stream has lost sync with the dictionary",
				commandDescription(cmd), elem+1, kind.description())
		}

		switch cmd {
		case cmdRaw:
			b, poff, ok := st.payload()
			if !ok {
				return nil, 0, dataError(errors.PULSE_SPSS_DATA_TRUNCATED, poff,
					"the compressed stream ends with a verbatim-value command (253) whose 8-byte value is missing; only %d byte(s) remain",
					len(st.src)-st.pos)
			}
			out = append(out, b...)
		case cmdSpaces:
			out = append(out, spacesElement[:]...)
		case cmdSysmis:
			out = append(out, sysmis[:]...)
		default:
			var b [elementSize]byte
			plan.bo.PutUint64(b[:], math.Float64bits(commandValue(cmd, bias)))
			out = append(out, b[:]...)
		}

		elem++
		if elem == len(kinds) {
			elem = 0
		}
	}

	if elem != 0 {
		return nil, 0, dataError(errors.PULSE_SPSS_DATA_TRUNCATED, st.offset(st.pos),
			"the compressed stream ends %d element(s) into a case of %d element(s); a case has no framing other than its element count, so a partial one cannot be read",
			elem, len(kinds))
	}
	return out, len(out) / plan.stride, nil
}

// decodedSizeHint is the capacity to open the output buffer at.
//
// The declared case count is the good hint and is normally exact. It is a
// writer's claim, though, so it is clamped to the largest output the input
// could possibly produce — one command byte is the cheapest an element gets,
// so len(src) elements of 8 bytes bounds it — which keeps a corrupt or
// hostile declaration from asking for an allocation the file cannot justify.
//
// The clamp is applied by DIVISION rather than by multiplying and comparing.
// A record 7/16 count is a full int64 the file supplies, and
// declared*stride overflows int64 for counts above about 2^58 — landing on a
// NEGATIVE product, which sails past a "> max" test and reaches make() as a
// negative capacity. E3-S5's dictionary corruption sweep found exactly that:
// one byte flipped inside the 7/16 payload panicked the decoder with
// "makeslice: cap out of range". Dividing first cannot overflow, so no
// declared value reaches the multiplication unless its product is already
// known to fit.
func decodedSizeHint(d *dictionary, plan *dataPlan, srcLen int) int {
	max := srcLen * elementSize
	declared, ok := declaredCaseCount(d)
	if !ok || declared <= 0 || plan.stride <= 0 {
		return 0
	}
	if declared > int64(max)/int64(plan.stride) {
		return max
	}
	return int(declared * int64(plan.stride))
}
