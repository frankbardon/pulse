package descriptor

import (
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/tablecall"
	"github.com/frankbardon/pulse/internal/windowparams"
	"github.com/frankbardon/pulse/internal/winformula"
	"github.com/frankbardon/pulse/types"
)

// WindowFormulaForwardReason is the PULSE_WINDOW_INVALID details.reason
// of a WIN_FORMULA expression reading a window label that is not on the
// row yet: its own output, or a later window's.
const WindowFormulaForwardReason = "forward_reference"

// checkWindowFormula judges WIN_FORMULA window i's params.expression at
// its pipeline point, on both sides (the runtime raises this walk's
// first entry before any window runs):
//
//   - each column it reads (winformula.Reads) is a column of the rows
//     the window runs over — the output row's group keys and
//     aggregation labels, or the record columns when the windows run
//     over the records — or an EARLIER window's label (the windows-first
//     aggregate rule over aggLabels, then w.check — the judgement every
//     window slot's names get). A name that is this
//     window's own label or a later window's is PULSE_WINDOW_INVALID
//     reason forward_reference; any other unknown name is the
//     SERVICE_VALIDATION a window's field reports (windowMissing);
//   - once every name resolves, the expression compiles
//     (winformula.Compile, the engine factory's compile) — over the
//     record rows with every schema field typed as its row cell
//     (winformula.Columns), over the output rows with each built-in
//     group key and aggregation label typed as its cell
//     (winformula.OutputColumns), so arithmetic on a categorical label
//     or a label key is refused here, not at the first row — against the
//     walk's request-bound declarations less coef / sweep_axis /
//     response (a window row reads no fitted model), and its literal
//     lookup() / range_label() calls are judged against the instance's
//     tables (tablecall.Refusal), as an ATTR_FORMULA's are.
//
// A slot whose params the shared reading refuses (windowparams.Formula:
// no expression) is that reading's refusal, reported by validateWindows
// and the factory; nothing more is judged here.
//
// aggLabels is the windows-first aggregation label set (nil otherwise):
// taken as a map, not the caller's closure, so the walk's per-request
// closures stay off the heap.
func (w *fieldRefWalk) checkWindowFormula(req *types.Request, i int, win *types.Window, aggLabels map[string]bool) {
	src, ref := windowparams.Formula(win)
	if ref != nil {
		return
	}
	idx := strconv.Itoa(i)
	before := len(w.out)
	reads, _ := winformula.Reads(src)
	for _, name := range reads {
		if j, ok := laterWindowLabel(req, i, name); ok && !w.cols[name] {
			w.record(name)
			w.out = append(w.out, windowFormulaForward(i, j, name, req.Windows[j]))
			continue
		}
		if aggLabels[name] && !w.cols[name] {
			w.out = append(w.out, windowsFirstReadsAggregate(i, name))
			continue
		}
		w.check(name, func() *errors.CodedError {
			return w.windowMissing(req, errors.SERVICE_VALIDATION, "window["+idx+"] ("+string(win.Type)+"): params.expression column "+name, name,
				map[string]any{"window_index": i, "field": name, "type": string(win.Type), "param": winformula.Param})
		})
	}
	if len(w.out) > before {
		return
	}
	bound := w.compile.bound
	bound.Coef, bound.SweepAxis, bound.Response = nil, nil, nil
	// Over the record rows (no output-row swap at step 5: w.rowCols
	// nil) every schema field is typed as its row cell
	// (winformula.Columns); over the output rows each built-in group key
	// and aggregation label as the cell the engine writes
	// (winformula.OutputColumns, each slot under its resolved weight —
	// the instance default included) — as the engine's applyWindows
	// types them.
	cols := winformula.Columns(w.compile.schema, w.rowCols == nil)
	if w.rowCols != nil {
		cols = winformula.OutputColumns(req, w.inst.Hidden, w.inst.DefaultWeight())
	}
	prog, ce := winformula.Compile(src, cols, bound, w.cols[exprenv.ParamsName])
	if ce != nil {
		w.out = append(w.out, ce)
		return
	}
	if ce := tablecall.Refusal(prog.VM().Node(), ExprTables(w.snap, w.inst)); ce != nil {
		w.out = append(w.out, ce)
	}
}

// laterWindowLabel reports the first window j >= i of req whose label is
// name: a column window i cannot read, because window j writes it at or
// after window i's turn.
func laterWindowLabel(req *types.Request, i int, name string) (int, bool) {
	for j := i; j < len(req.Windows); j++ {
		if win := req.Windows[j]; win != nil && windowLabel(win) == name {
			return j, true
		}
	}
	return 0, false
}

// windowFormulaForward is the forward_reference refusal of WIN_FORMULA
// window i reading name, the label window j writes.
func windowFormulaForward(i, j int, name string, writer *types.Window) *errors.CodedError {
	idx, jdx := strconv.Itoa(i), strconv.Itoa(j)
	cause := "the output of window[" + jdx + "] (" + string(writer.Type) + "), which runs after it; move window[" + jdx + "] before window[" + idx + "]"
	if i == j {
		cause = "its own output"
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_WINDOW_INVALID,
		"window["+idx+"] (WIN_FORMULA): params.expression reads "+strconv.Quote(name)+", "+cause+
			"; a window formula reads only its row's columns and the outputs of the windows before it",
		map[string]any{"window_index": i, "type": string(types.WIN_FORMULA), "param": winformula.Param,
			"field": name, "reason": WindowFormulaForwardReason, "writer_window_index": j})
}
