package optgrad

import (
	"fmt"
	"slices"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
)

// BoundRules are the derivative rules of an optimize block's
// request-bound functions over b — the implementations the block's VM
// program calls, so the dual's values are the VM's bit for bit:
//
//   - coef(fit, term[, stat]), sweep_axis(fit, axis) and
//     result(slot, aggregation) read figures that do not move with the
//     decision variables (their arguments are strings), so their
//     partials are all 0 — a constant term;
//   - response(fit, channel, spend[, geo]) is ∂/∂spend =
//     b.ResponseSlope (the channel curve's slope times the adstock
//     record's steady-state multiplier; model.ResponseSlope), 0 for
//     the string arguments;
//   - an embedder ExprFunction named in differentiable carries its
//     Derivative hook (b.Extensions' NamedFunc.Derivative); one not
//     named there has no rule — even over a fit reader it shadows — so
//     a block calling it falls back to finite differences.
//
// Each reader has a rule exactly when b declares it (predict's
// declaration-only bound and the runtime's agree on that), and an
// embedder function exactly when differentiable names it (both sides
// derive it from the same registration), so predict's fallback
// judgement and the runtime's are one. A rule's derivative is only ever
// called at run time: a declaration-only bound's missing ResponseSlope
// or Derivative is an internal fault, never a figure.
func BoundRules(b exprenv.Bound, differentiable []string) map[string]Rule {
	out := map[string]Rule{}
	constant := func(impl func(args ...any) (any, error)) Rule {
		return Rule{Impl: impl, Partials: func(a []float64, _ float64) []float64 { return make([]float64, len(a)) }}
	}
	if b.Coef != nil {
		out[exprenv.CoefFunc] = constant(b.Coef)
	}
	if b.SweepAxis != nil {
		out[exprenv.SweepAxisFunc] = constant(b.SweepAxis)
	}
	if b.Result != nil {
		out[exprenv.ResultFunc] = constant(b.Result)
	}
	if b.Response != nil {
		slope := b.ResponseSlope
		out[exprenv.ResponseFunc] = Rule{Impl: b.Response, Derivative: func(args []any, _ float64) ([]float64, error) {
			if slope == nil {
				return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
					"response() derivative called without a bound slope: a declaration-only bound is never differentiated")
			}
			d, err := slope(args...)
			if err != nil {
				return nil, err
			}
			p := make([]float64, len(args))
			p[2] = d
			return p, nil
		}}
	}
	for _, fn := range b.Extensions {
		if fn.Fn == nil {
			continue
		}
		if !slices.Contains(differentiable, fn.Name) {
			// Registered after the readers (exprenv.Bound.Options), an
			// embedder function owns its name: without a Derivative the
			// name has no rule.
			delete(out, fn.Name)
			continue
		}
		out[fn.Name] = Rule{Impl: exprenv.Func(fn.Fn), Derivative: extensionDerivative(fn.Name, fn.Derivative)}
	}
	return out
}

// extensionDerivative is an embedder Derivative hook as a rule's: its
// error and a partial count that does not match the call become a
// PROCESSING_RUNTIME fault naming the function (fatal to the solve, as
// the function's own error at an iterate is).
func extensionDerivative(name string, hook func(args ...any) ([]float64, error)) func(args []any, out float64) ([]float64, error) {
	return func(args []any, _ float64) ([]float64, error) {
		if hook == nil {
			return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
				fmt.Sprintf("expr function %q derivative called without a bound hook: a declaration-only bound is never differentiated", name))
		}
		p, err := hook(args...)
		if err != nil {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_RUNTIME,
				fmt.Sprintf("expr function %q Derivative failed: %v", name, err),
				map[string]any{"function": name})
		}
		if len(p) != len(args) {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_RUNTIME,
				fmt.Sprintf("expr function %q Derivative returned %d partials for a call of %d arguments; return one per argument", name, len(p), len(args)),
				map[string]any{"function": name, "partials": len(p), "arguments": len(args)})
		}
		return p, nil
	}
}
