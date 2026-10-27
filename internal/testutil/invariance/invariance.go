// Package invariance is the shared worker-invariance harness: one
// operator-level way to prove that a request answers the same under
// every execution knob — DecodeWorkers, ShardWorkers, a single file
// against a shard archive of the same rows, streaming against buffered.
// Test code only: nothing outside a _test.go file may import it
// (TestInvariance_TestOnlyImporters).
//
// A family is one request shape (or a batch of request variants the
// family folds into one request) plus a Run that executes it under a
// Knobs cell and returns the arm-invariant part of the answer. The
// harness runs the family over its Groups: each group's cells are
// compared against the group's reference cell with the family's Rule
// (BitExact unless the family declares a documented tolerance), and
// every cell that asks for a parallel arm must report that arm, so no
// cell ever compares serial against serial.
//
// Tiers. The parallel-decode arm engages only on a single-file cohort
// of at least limits.ParallelDecodeRecordThreshold records (about
// 100,000), and there is no knob to lower it: a cohort below it runs
// DecodeWorkers > 1 serially. So the cheap groups (ArchiveGroup,
// BufferedCell — File and Archive layouts, a few thousand rows) fit the
// fast tier, and DecodeGroup — the BigFile layout, above the threshold —
// belongs in the heavy tier (`//go:build heavy`). The harness does not
// build fixtures: a family maps each Layout to a cohort it owns.
package invariance

import (
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
)

// Layout names the cohort a cell reads. The family maps it to a path.
type Layout string

const (
	// File is a single-file cohort below the parallel-decode threshold:
	// the reference layout and the shard archive's twin (same rows, same
	// order).
	File Layout = "file"
	// Archive is a shard archive of File's rows, split unevenly (shard
	// edges inside merge blocks; a 1-row or empty shard where the family
	// can).
	Archive Layout = "archive"
	// BigFile is a single-file cohort at or above the parallel-decode
	// threshold (heavy tier only).
	BigFile Layout = "big_file"
)

// Mode selects the execution arm a family forces, when it can.
type Mode string

const (
	// Default leaves dispatch to the engine (streaming when the request
	// streams).
	Default Mode = ""
	// Buffered asks the family to force the buffered arm (typically a
	// non-streamable slot it strips from the answer again).
	Buffered Mode = "buffered"
)

// Knobs is one execution configuration.
type Knobs struct {
	Layout        Layout
	DecodeWorkers int
	ShardWorkers  int
	Mode          Mode
}

func (k Knobs) String() string {
	s := fmt.Sprintf("%s/decode%d_shard%d", k.Layout, k.DecodeWorkers, k.ShardWorkers)
	if k.Mode != Default {
		s += "_" + string(k.Mode)
	}
	return s
}

// Cell is a Knobs plus the arm the run must report ("" = unchecked).
type Cell struct {
	Knobs
	Arm observe.Arm
}

// Group is a reference cell and the cells compared against it.
type Group struct {
	Name  string
	Ref   Cell
	Cells []Cell
}

// Result is one run: the arm-invariant answer and the arm that ran.
type Result[R any] struct {
	Answer R
	Arm    observe.Arm
}

// Rule compares two answers and returns the paths where they differ
// (nil = equal).
type Rule func(got, want any) []string

// Family is one request shape run over its groups.
type Family[R any] struct {
	Name   string
	Groups []Group
	// Run executes the family under k. It fails t on a run error.
	Run func(t *testing.T, k Knobs) Result[R]
	// Check guards the reference answer against degeneracy (no rows, no
	// buckets, no nulls where the fixture promises them), so parity is
	// never parity over nothing; k is the reference cell's knobs (the
	// layout fixes the row count an anchor recomputes). Required.
	Check func(t *testing.T, k Knobs, ref R)
	// Compare is the family's comparison rule; nil = BitExact.
	Compare Rule
}

// Shard and decode worker counts. Shard counts vary the partition of an
// archive (and so the merge-tree shape: even, odd, more workers than
// shards); DecodeWorkers over an archive never takes the per-segment
// decode arm (each shard decodes whole), so two archive cells carry
// DecodeWorkers 7 to pin that it stays out of the answer.
var (
	ArchiveShardWorkers = []int{2, 3, 8}
	BigFileDecoders     = []int{2, 3, 7, 8}
)

// ArchiveGroup compares the shard archive under every ShardWorkers count
// (and two DecodeWorkers 7 cells) against its single-file twin read
// serially. Cheap: fast tier.
func ArchiveGroup() Group {
	g := Group{Name: "archive", Ref: Cell{Knobs: Knobs{Layout: File, DecodeWorkers: 1, ShardWorkers: 1}}}
	g.Cells = append(g.Cells, Cell{Knobs: Knobs{Layout: Archive, DecodeWorkers: 1, ShardWorkers: 1}})
	for _, sw := range ArchiveShardWorkers {
		g.Cells = append(g.Cells, Cell{Knobs: Knobs{Layout: Archive, DecodeWorkers: 1, ShardWorkers: sw}, Arm: observe.ArmShardParallel})
	}
	g.Cells = append(g.Cells,
		Cell{Knobs: Knobs{Layout: Archive, DecodeWorkers: 7, ShardWorkers: 1}},
		Cell{Knobs: Knobs{Layout: Archive, DecodeWorkers: 7, ShardWorkers: 3}, Arm: observe.ArmShardParallel})
	return g
}

// BufferedCell is the File layout on the forced buffered arm; append it
// to a group whose reference streams.
func BufferedCell() Cell {
	return Cell{Knobs: Knobs{Layout: File, DecodeWorkers: 1, ShardWorkers: 1, Mode: Buffered}, Arm: observe.ArmBuffered}
}

// DecodeGroup compares layout (a single file above the parallel-decode
// threshold) under every DecodeWorkers count against its serial read.
// Heavy tier.
func DecodeGroup(name string, layout Layout) Group {
	g := Group{Name: name, Ref: Cell{Knobs: Knobs{Layout: layout, DecodeWorkers: 1, ShardWorkers: 1}}}
	for _, dw := range BigFileDecoders {
		g.Cells = append(g.Cells, Cell{Knobs: Knobs{Layout: layout, DecodeWorkers: dw, ShardWorkers: 1}, Arm: observe.ArmParallelDecode})
	}
	return g
}

// Validate refuses a family the harness cannot prove anything with: no
// Run or Check, no group, an empty group, or a cell that asks for a
// parallel arm (ShardWorkers > 1 over Archive, DecodeWorkers > 1 over
// BigFile) without naming the arm it must take.
func Validate[R any](f Family[R]) error {
	if f.Run == nil || f.Check == nil {
		return fmt.Errorf("family %q: Run and Check are required", f.Name)
	}
	if len(f.Groups) == 0 {
		return fmt.Errorf("family %q: no group", f.Name)
	}
	for _, g := range f.Groups {
		if len(g.Cells) == 0 {
			return fmt.Errorf("family %q group %q: no cell", f.Name, g.Name)
		}
		for _, c := range append([]Cell{g.Ref}, g.Cells...) {
			parallel := (c.Layout == Archive && c.ShardWorkers > 1) || (c.Layout == BigFile && c.DecodeWorkers > 1)
			if parallel && c.Arm == "" {
				return fmt.Errorf("family %q group %q cell %s: a parallel cell must name the arm it takes", f.Name, g.Name, c.Knobs)
			}
		}
	}
	return nil
}

// Verdict is one cell's comparison against its reference: the arm
// mismatch (if any) followed by the answer's differing paths.
func Verdict[R any](rule Rule, c Cell, ref, got Result[R]) []string {
	if rule == nil {
		rule = BitExact
	}
	var out []string
	if c.Arm != "" && got.Arm != c.Arm {
		out = append(out, fmt.Sprintf("ran on arm %q, want %q", got.Arm, c.Arm))
	}
	return append(out, rule(got.Answer, ref.Answer)...)
}

// Run runs f over its groups: one parallel subtest per group, which
// runs the reference cell, checks it, and compares every cell against it
// in a parallel subtest of its own.
func Run[R any](t *testing.T, f Family[R]) {
	t.Helper()
	if err := Validate(f); err != nil {
		t.Fatal(err)
	}
	t.Run(f.Name, func(t *testing.T) {
		t.Parallel()
		for _, g := range f.Groups {
			t.Run(g.Name, func(t *testing.T) {
				t.Parallel()
				ref := f.Run(t, g.Ref.Knobs)
				if g.Ref.Arm != "" && ref.Arm != g.Ref.Arm {
					t.Fatalf("reference %s ran on arm %q, want %q", g.Ref.Knobs, ref.Arm, g.Ref.Arm)
				}
				f.Check(t, g.Ref.Knobs, ref.Answer)
				for _, c := range g.Cells {
					t.Run(c.Knobs.String(), func(t *testing.T) {
						t.Parallel()
						if d := Verdict(f.Compare, c, ref, f.Run(t, c.Knobs)); len(d) > 0 {
							t.Errorf("%s differs from %s:\n  %s", c.Knobs, g.Ref.Knobs, strings.Join(d, "\n  "))
						}
					})
				}
			})
		}
	})
}

// maxDiffs caps the paths a rule reports.
const maxDiffs = 12

// BitExact compares every float by its bits (NaN included — both sides
// are computed live in one process) and everything else exactly. A nil
// slice or map differs from an empty one (absent ≠ empty on the wire).
func BitExact(got, want any) []string {
	return Diff(got, want, func(a, b float64) bool { return math.Float64bits(a) == math.Float64bits(b) })
}

// Within compares floats to |a-b| <= abs + rel·max(|a|,|b|) (NaN equals
// NaN; infinities must match) and everything else exactly — for a
// family whose merge is documented ULP-bounded rather than bit-exact.
func Within(rel, abs float64) Rule {
	return func(got, want any) []string {
		return Diff(got, want, func(a, b float64) bool {
			switch {
			case math.IsNaN(a) || math.IsNaN(b):
				return math.IsNaN(a) && math.IsNaN(b)
			case math.IsInf(a, 0) || math.IsInf(b, 0):
				return a == b
			}
			return math.Abs(a-b) <= abs+rel*math.Max(math.Abs(a), math.Abs(b))
		})
	}
}

// Diff walks got and want in step and returns up to maxDiffs paths where
// they differ, floats compared with eq. Unexported fields are compared
// too; funcs and channels are skipped.
func Diff(got, want any, eq func(a, b float64) bool) []string {
	d := &differ{eq: eq}
	d.walk("$", reflect.ValueOf(got), reflect.ValueOf(want))
	if d.more > 0 {
		d.out = append(d.out, fmt.Sprintf("... and %d more", d.more))
	}
	return d.out
}

type differ struct {
	eq   func(a, b float64) bool
	out  []string
	more int
}

func (d *differ) add(path, format string, args ...any) {
	if len(d.out) >= maxDiffs {
		d.more++
		return
	}
	d.out = append(d.out, path+": "+fmt.Sprintf(format, args...))
}

func (d *differ) walk(path string, g, w reflect.Value) {
	if !g.IsValid() || !w.IsValid() {
		if g.IsValid() != w.IsValid() {
			d.add(path, "present = %v, want %v", g.IsValid(), w.IsValid())
		}
		return
	}
	if g.Type() != w.Type() {
		d.add(path, "type %s, want %s", g.Type(), w.Type())
		return
	}
	switch g.Kind() {
	case reflect.Interface, reflect.Pointer:
		if g.IsNil() || w.IsNil() {
			if g.IsNil() != w.IsNil() {
				d.add(path, "nil = %v, want %v", g.IsNil(), w.IsNil())
			}
			return
		}
		d.walk(path, g.Elem(), w.Elem())
	case reflect.Struct:
		for i := 0; i < g.NumField(); i++ {
			d.walk(path+"."+g.Type().Field(i).Name, g.Field(i), w.Field(i))
		}
	case reflect.Slice:
		if g.IsNil() != w.IsNil() {
			d.add(path, "nil = %v, want %v (absent differs from empty)", g.IsNil(), w.IsNil())
			return
		}
		fallthrough
	case reflect.Array:
		if g.Len() != w.Len() {
			d.add(path, "len %d, want %d", g.Len(), w.Len())
			return
		}
		for i := 0; i < g.Len(); i++ {
			d.walk(fmt.Sprintf("%s[%d]", path, i), g.Index(i), w.Index(i))
		}
	case reflect.Map:
		if g.IsNil() != w.IsNil() {
			d.add(path, "nil = %v, want %v (absent differs from empty)", g.IsNil(), w.IsNil())
			return
		}
		keys := map[string]reflect.Value{}
		for _, k := range append(g.MapKeys(), w.MapKeys()...) {
			keys[fmt.Sprint(k)] = k
		}
		names := make([]string, 0, len(keys))
		for n := range keys {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			gv, wv := g.MapIndex(keys[n]), w.MapIndex(keys[n])
			p := fmt.Sprintf("%s[%s]", path, n)
			if !gv.IsValid() || !wv.IsValid() {
				d.add(p, "key present = %v, want %v", gv.IsValid(), wv.IsValid())
				continue
			}
			d.walk(p, gv, wv)
		}
	case reflect.Float32, reflect.Float64:
		if a, b := g.Float(), w.Float(); !d.eq(a, b) {
			d.add(path, "%.17g (%#016x), want %.17g (%#016x)", a, math.Float64bits(a), b, math.Float64bits(b))
		}
	case reflect.Complex64, reflect.Complex128:
		if a, b := g.Complex(), w.Complex(); !d.eq(real(a), real(b)) || !d.eq(imag(a), imag(b)) {
			d.add(path, "%v, want %v", a, b)
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if g.Int() != w.Int() {
			d.add(path, "%d, want %d", g.Int(), w.Int())
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		if g.Uint() != w.Uint() {
			d.add(path, "%d, want %d", g.Uint(), w.Uint())
		}
	case reflect.Bool:
		if g.Bool() != w.Bool() {
			d.add(path, "%v, want %v", g.Bool(), w.Bool())
		}
	case reflect.String:
		if g.String() != w.String() {
			d.add(path, "%q, want %q", g.String(), w.String())
		}
	}
}

// LayoutFree is resp with the two facts that legitimately differ
// between a single file and its shard archive blanked —
// Metadata.CohortFile and Components.Run.ShardCount — so a whole
// response can be compared across layouts. resp is not modified.
func LayoutFree(resp *types.Response) *types.Response {
	if resp == nil {
		return nil
	}
	out := *resp
	if resp.Metadata != nil {
		md := *resp.Metadata
		md.CohortFile = ""
		out.Metadata = &md
	}
	if resp.Components != nil {
		c := *resp.Components
		if c.Run != nil {
			run := *c.Run
			run.ShardCount = 0
			c.Run = &run
		}
		out.Components = &c
	}
	return &out
}
