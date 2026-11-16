package postfit

import (
	"encoding/json"
	"maps"
	"slices"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/resultcall"
	"github.com/frankbardon/pulse/types"
)

// Env is what every entry of a batch reads: the bound fits, the slot
// results and the optimize solutions.
type Env struct {
	// Fits is the batch's bound set (every fit, failed ones recorded).
	Fits *model.Set
	// Results reads finished slots (every slot has run).
	Results *resultcall.Reader
	// Optimizations are the optimize blocks' results, declaration order.
	Optimizations []types.OptimizeResult
}

// Outcome is one entry's run: its result, and whether it failed alone.
type Outcome struct {
	Result types.PostFitResult
	Failed bool
}

// Run runs entry i (spec) with op. A fit it names that failed on its
// data fails the entry alone with that fit's code before the operator
// runs; an operator error fails it alone (SlotErrorOf). An output its
// declaration does not admit is the fatal
// PULSE_EXTENSION_POSTFIT_OUTPUT_MISMATCH; so is any other fit lookup
// fault (an invariant: Check admitted every name). The caller recovers
// a panic.
func Run(i int, spec *types.PostFitSpec, op *Operator, env Env) (Outcome, error) {
	in := &input{spec: spec, env: env, fits: make(map[string]*fitHandle, len(spec.Fits))}
	for _, name := range spec.Fits {
		if se, failed := env.Fits.Failed(name); failed {
			return Outcome{Result: Failure(spec, slotError(model.FailedFitRefusal(name, se))), Failed: true}, nil
		}
		b, err := env.Fits.Lookup(name)
		if err != nil {
			return Outcome{}, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
				"post_fit entry "+spec.Label+": fit "+name+" is not bound: "+err.Error(),
				map[string]any{Slot: i, "label": spec.Label, "fit": name})
		}
		in.fits[name] = &fitHandle{b: b, set: env.Fits}
	}
	out, err := op.Run(in)
	if err != nil {
		return Outcome{Result: Failure(spec, SlotErrorOf(spec, err)), Failed: true}, nil
	}
	if cerr := CheckOutput(i, spec.Label, op.Decl, out); cerr != nil {
		return Outcome{}, cerr
	}
	return Outcome{Result: Wire(spec, out)}, nil
}

func slotError(ce *errors.CodedError) *types.SlotError {
	se := &types.SlotError{Code: string(ce.Code), Message: ce.Message}
	if len(ce.Details) > 0 {
		se.Details = maps.Clone(ce.Details)
	}
	return se
}

// input is extend.PostFitInput over one entry.
type input struct {
	spec *types.PostFitSpec
	env  Env
	fits map[string]*fitHandle
}

func (in *input) Label() string { return in.spec.Label }

func (in *input) Params() json.RawMessage {
	if len(in.spec.Params) == 0 {
		return nil
	}
	return slices.Clone(in.spec.Params)
}

func (in *input) Fits() []string { return slices.Clone(in.spec.Fits) }

func (in *input) Fit(name string) extend.PostFitFit {
	if h, ok := in.fits[name]; ok {
		return h
	}
	return nil
}

func (in *input) Result(slot, aggregation string) (float64, error) {
	v, cerr := in.env.Results.Value(slot, aggregation)
	if cerr != nil {
		return 0, cerr
	}
	return v, nil
}

func (in *input) Optimizations() []extend.PostFitSolution {
	out := make([]extend.PostFitSolution, len(in.env.Optimizations))
	for k, r := range in.env.Optimizations {
		s := extend.PostFitSolution{Name: r.Name}
		if r.Error != nil {
			s.Failed, s.ErrorCode = true, r.Error.Code
			out[k] = s
			continue
		}
		s.Objective, s.Converged, s.Status = r.Objective, r.Converged, r.Status
		s.Variables = make(map[string]float64, len(r.Variables))
		for _, v := range r.Variables {
			s.Variables[v.Name] = v.Value
		}
		out[k] = s
	}
	return out
}

// fitHandle is extend.PostFitFit over one bound regression.
type fitHandle struct {
	b   *model.Bound
	set *model.Set
}

func (h *fitHandle) Name() string               { return h.b.Name }
func (h *fitHandle) Type() types.RegressionType { return h.b.Type }
func (h *fitHandle) Target() string             { return h.b.Target }

func (h *fitHandle) Coefficients() map[string]float64 {
	if h.b.Result == nil {
		return map[string]float64{}
	}
	return orEmpty(maps.Clone(h.b.Result.Coefficients))
}

func (h *fitHandle) StdErrors() map[string]float64 {
	if h.b.Result == nil {
		return map[string]float64{}
	}
	return orEmpty(maps.Clone(h.b.Result.StdErrors))
}

func orEmpty(m map[string]float64) map[string]float64 {
	if m == nil {
		return map[string]float64{}
	}
	return m
}

func (h *fitHandle) Vcov() *types.MatrixValues {
	v := h.b.Vcov
	if v == nil {
		return nil
	}
	c := &types.MatrixValues{Kind: v.Kind, Encoding: v.Encoding, RowKeys: slices.Clone(v.RowKeys),
		ColumnKeys: slices.Clone(v.ColumnKeys), Labels: slices.Clone(v.Labels), Values: make([][]float64, len(v.Values))}
	for r, row := range v.Values {
		c.Values[r] = slices.Clone(row)
	}
	return c
}

func (h *fitHandle) AdstockChannels() []string {
	return slices.Sorted(maps.Keys(h.b.Adstocks))
}

func (h *fitHandle) Adstock(channel string) (extend.PostFitAdstock, bool) {
	a, ok := h.b.Adstocks[channel]
	if !ok {
		return extend.PostFitAdstock{}, false
	}
	out := extend.PostFitAdstock{Kernel: a.Kernel, Decay: orNaN(a.Decay), Peak: orNaN(a.Peak),
		Shape: orNaN(a.Shape), Scale: orNaN(a.Scale), MaxLag: a.MaxLag, Normalize: a.Normalize}
	if w, err := a.Weights(); err == nil {
		out.Weights = slices.Clone(w)
	}
	return out, true
}

func (h *fitHandle) Draws() (extend.PostFitDraws, bool) {
	if h.b.Result == nil {
		return extend.PostFitDraws{}, false
	}
	if _, ok := h.b.Result.Outputs[model.MMMDrawsKey]; !ok {
		return extend.PostFitDraws{}, false
	}
	d, err := h.set.MMMDrawsOf(h.b.Name)
	if err != nil {
		return extend.PostFitDraws{}, false
	}
	names := make([]string, len(d.Index))
	for n, j := range d.Index {
		names[j] = n
	}
	cols := make([][]float64, len(d.Cols))
	for j, c := range d.Cols {
		cols[j] = slices.Clone(c)
	}
	return extend.PostFitDraws{Names: names, Draws: cols, CredibleLevel: d.Level}, true
}
