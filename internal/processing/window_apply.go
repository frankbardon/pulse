package processing

import (
	"context"
	"slices"

	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/scalarslot"
	"github.com/frankbardon/pulse/internal/windowscalar"

	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/internal/processing/window"
	"github.com/frankbardon/pulse/internal/shareparams"
	"github.com/frankbardon/pulse/internal/windowparams"
	"github.com/frankbardon/pulse/internal/winformula"
	"github.com/frankbardon/pulse/types"
)

// applyWindows runs req's windows over rows, after refusing every
// window whose configuration the shared window reading
// (internal/windowparams) refuses, and every window whose value field the accepted-types table (internal/fieldaccept)
// does not accept — a categorical's dictionary index, a set's bitmask,
// decimal128, packed_bool, datetime. The refusal is predict's
// (validateWindows reports the same *errors.CodedError) and is raised
// before any row is windowed, empty input included. A type the
// instance does not resolve is left to ApplyWithExt's unknown-window
// error, as predict reports an unknown type before its field. orders is
// the grouped run's grouper key orders (groupKeyOrders): an order_by on
// a grouper key column follows its grouper's key order. Nil on a
// record-row run (no grouper key column), byte-identically. copies,
// when set, hears every built-in copy window's (WIN_LAG / WIN_LEAD)
// copied cell (window.ApplyObserved) — the record-row run's
// u64RowWords.copyObserver; nil observes nothing. overRecords reports
// that rows are the record rows (a windows-only run, the windows-first
// pass, Materialize), not req's grouped or aggregated output rows: a
// WIN_FORMULA then types its record columns (winformula.Columns), else
// req's built-in group keys and aggregation labels
// (winformula.OutputColumns).
//
// A window's scalar-expression params (internal/windowscalar) are
// resolved first, against the request's params and bound models, and
// the slot the reading and the factory see carries the numbers.
func (p *Processor) applyWindows(ctx context.Context, rows []map[string]any, req *types.Request, orders window.KeyOrders, copies window.CopyObserver, overRecords bool) error {
	windows, err := p.resolveWindowScalars(req.Windows)
	if err != nil {
		return err
	}
	for _, w := range windows {
		if w == nil {
			continue
		}
		if _, ok := p.exts.LookupWindow(w.Type); !ok {
			continue
		}
		// The slot's configuration, one reading with predict
		// (internal/windowparams): the first rule predict reports, with
		// its code and sentence — an embedder's window judged on the
		// type-neutral rules, as predict judges it.
		if ref := windowparams.First(w); ref != nil {
			return ref.Coded()
		}
		if cerr := fieldaccept.Check(string(w.Type), fieldaccept.SlotField, w.Field, p.schema); cerr != nil {
			return cerr
		}
		// WIN_LIFT's base column, judged like its field (predict
		// validateWindows reports the same error). A slot the shared
		// reading refuses is left to the factory's refusal.
		if w.Type == types.WIN_LIFT {
			if spec, ref := shareparams.Resolve(w); ref == nil {
				if cerr := fieldaccept.Check(string(w.Type), fieldaccept.SlotBase, spec.Base, p.schema); cerr != nil {
					return cerr
				}
			}
		}
	}
	// A WIN_FORMULA compiles against the registry's request-bound
	// declarations (params, ExprFunctions, lookup, range_label) — the
	// set predict's field-reference walk declares with placeholders —
	// and types the rows' columns as predict's walk does: each schema
	// field over the record rows (overRecords), each built-in group key
	// and aggregation label over the output rows. Built only when a
	// WIN_FORMULA runs.
	opts := window.WindowOptions{ExprBound: p.exts.exprBound()}
	if hasFormulaWindow(windows) {
		if overRecords {
			opts.Columns = winformula.Columns(p.schema, true)
		} else {
			opts.Columns = winformula.OutputColumns(req, p.exts.isHidden, p.defaultWeight)
		}
	}
	return window.ApplyWith(ctx, rows, windows, p.exts.LookupWindow, orders, copies, opts)
}

// hasFormulaWindow reports whether windows carry a WIN_FORMULA slot.
func hasFormulaWindow(windows []*types.Window) bool {
	for _, w := range windows {
		if w != nil && w.Type == types.WIN_FORMULA {
			return true
		}
	}
	return false
}

// resolveWindowScalars is the runtime's windowscalar pass over windows:
// every scalar-expression param evaluated against the request's params,
// its bound models (coef(), sweep_axis()), the instance's tables and
// the embedder's functions, and written as the number the reading
// judges. The first refusal is raised (predict reports it first for its
// window). windows comes back as itself when no window carries an
// expression; otherwise a copy (the caller's slots are never mutated).
// A type the instance does not resolve is left to the unknown-window
// error, as predict reports an unknown type first.
func (p *Processor) resolveWindowScalars(windows []*types.Window) ([]*types.Window, error) {
	return ResolveWindowScalars(p.exts, p.models, windows)
}

// ResolveWindowScalars is the runtime's windowscalar pass over windows
// for a request run over exts and models — the one the Processor runs
// before applying them, so a caller reading the windows a run applied
// (a fit's adstock lineage, model.AdstockLineage) sees the very numbers
// the run read.
func ResolveWindowScalars(exts *ExtensionRegistry, models *model.Set, windows []*types.Window) ([]*types.Window, error) {
	var out []*types.Window
	var env *scalarslot.Env
	var bound exprenv.Bound
	for i, w := range windows {
		if w == nil || len(windowscalar.Exprs(w)) == 0 {
			continue
		}
		if _, ok := exts.LookupWindow(w.Type); !ok {
			continue
		}
		if env == nil {
			e := scalarEnvOf(exts, models)
			env, bound = &e, exts.ExprBoundWithModels(models)
		}
		res, refs := windowscalar.Resolve(w, *env, bound, true)
		if len(refs) > 0 {
			return nil, refs[0].Coded()
		}
		if out == nil {
			out = slices.Clone(windows)
		}
		out[i] = res.Window
	}
	if out == nil {
		return windows, nil
	}
	return out, nil
}

// scalarEnvOf is the scalar-slot environment of a request run over exts
// (its params fold, tables and expression functions) and models.
func scalarEnvOf(exts *ExtensionRegistry, models *model.Set) scalarslot.Env {
	env := scalarslot.Env{Params: exts.Params(), Fits: catalogOf(models), Tables: exts.TablesCatalog()}
	if exts != nil {
		for _, fn := range exts.ExprFunctions {
			env.Extensions = append(env.Extensions, fn.Name)
		}
		env.Lookup = len(exts.LookupTables) > 0
	}
	return env
}
