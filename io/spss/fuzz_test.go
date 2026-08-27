package spss

import (
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/spsstest"
)

// FuzzParseDictionary backs the "never a panic" acceptance criterion on the
// axis the truncation table cannot reach: arbitrary byte mutations, not just
// prefixes.
//
// The invariant is total. For ANY input, parseDictionary either succeeds or
// returns a *errors.CodedError from the PULSE_SPSS_DICT_* family carrying both
// the record it was reading and an in-range byte offset. It never panics, and
// it never returns a bare error — a caller that cannot switch on the code
// cannot tell "this is not a system file" from "this transfer was cut short".
//
// The seed corpus is generator output plus the shapes the parser has special
// cases for; `go test` runs the seeds on every build, and `go test -fuzz` can
// then mutate from there.
func FuzzParseDictionary(f *testing.F) {
	seeds := []spsstest.Spec{
		spsstest.ReferenceSpec(),
		{Vars: []spsstest.Var{{Name: "A"}}},
		{Vars: []spsstest.Var{{Name: "A", Width: 255}}},
		{
			Vars: []spsstest.Var{{Name: "WIDE", Width: 20}, {Name: "CODE", Width: 4}, {Name: "N", Label: "labelled"}},
			ValueLabels: []spsstest.ValueLabelSet{
				{Vars: []string{"CODE"}, Labels: []spsstest.ValueLabel{{Value: spsstest.Text("AB"), Label: "Alpha"}}},
				{Vars: []string{"N"}, Labels: []spsstest.ValueLabel{{Value: spsstest.Num(1), Label: "One"}}},
			},
			Cases: [][]spsstest.Value{{spsstest.Text("x"), spsstest.Text("AB"), spsstest.Num(1)}},
		},
	}
	for _, spec := range seeds {
		b, err := spsstest.Build(spec)
		if err != nil {
			f.Fatalf("spsstest.Build: %v", err)
		}
		f.Add(b)
		// A dictionary carrying the two record types the parser skips
		// rather than interprets.
		d, err := parseDictionary(b)
		if err != nil {
			f.Fatalf("parseDictionary: %v", err)
		}
		f.Add(splice(b, d.dataOffset-8, concat(documentRecord(1), extensionRecord(3, 4, 8))))
	}
	f.Add([]byte(nil))
	f.Add([]byte("$FL2"))
	f.Add(make([]byte, headerSize))

	f.Fuzz(func(t *testing.T, in []byte) {
		d, err := parseDictionary(in)
		if err == nil {
			if d.dataOffset < headerSize || d.dataOffset > len(in) {
				t.Fatalf("parse succeeded with dataOffset %d, outside %d..%d", d.dataOffset, headerSize, len(in))
			}
			if d.byteOrder == nil {
				t.Fatal("parse succeeded without fixing a byte order")
			}
			return
		}
		ce, ok := err.(*perr.CodedError)
		if !ok {
			t.Fatalf("error is %T, not *errors.CodedError: %v", err, err)
		}
		switch ce.Code {
		case perr.PULSE_SPSS_DICT_INVALID, perr.PULSE_SPSS_DICT_TRUNCATED:
		default:
			t.Fatalf("code = %s, want a PULSE_SPSS_DICT_* code", ce.Code)
		}
		assertDetails(t, ce, len(in))
	})
}
