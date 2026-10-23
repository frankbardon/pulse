package processing

import (
	"context"
	"fmt"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/processing/regression"
	"github.com/frankbardon/pulse/internal/totalcall"
	"github.com/frankbardon/pulse/types"
)

// regression_holdout.go is the engine half of RegressionSpec.Holdout:
// the buffered fit of a regression slot whose holdout predicate splits
// the filtered records into the rows it fits (predicate false), the rows
// it holds out and scores afterwards (true) and the rows it skips (null,
// counted in RegressionHoldout.NNull). The predicate reads the fully
// built record — the windows-over-records copy (windowRecordColumns) and
// the post-window attribute phase have run by the time the fits do. The
// name rules (scorable type, window availability, total() / coef(), the
// boolean compile) are the field-reference walk's (checkHoldout),
// shared with predict; the build here re-raises the same compile
// refusals, and a holdout always takes the buffered path
// (types.RegressionSpec.StreamableAs).

// fitRegressions fits every regression slot of req over filtered: the
// slots without a holdout in one batch over every record, exactly as
// before holdout existed (byte-identical), each holdout slot alone over
// its own train rows, then scored on its held-out rows. A slot's
// data-dependent refusal — its fit's, or no scorable held-out row —
// fails it alone (regressionSlotResults); anything else is fatal.
func (p *Processor) fitRegressions(ctx context.Context, req *types.Request, filtered []*Record) ([]*types.RegressionResult, error) {
	specs := req.Regressions
	holdout := false
	for _, s := range specs {
		if s != nil && s.Holdout != "" {
			holdout = true
			break
		}
	}
	if !holdout {
		fits, fitErrs, err := regression.FitEachBufferedWithContext(ctx, specs, p.schema, recordsAsRegressionRecords(filtered), p.exts.LookupRegression)
		if err != nil {
			return nil, err
		}
		return regressionSlotResults(specs, fits, fitErrs)
	}

	fits := make([]*types.RegressionResult, len(specs))
	fitErrs := make([]error, len(specs))
	var plain []*types.RegressionSpec
	var plainAt []int
	for i, s := range specs {
		if s == nil || s.Holdout == "" {
			plain = append(plain, s)
			plainAt = append(plainAt, i)
		}
	}
	if len(plain) > 0 {
		pf, pe, err := regression.FitEachBufferedWithContext(ctx, plain, p.schema, recordsAsRegressionRecords(filtered), p.exts.LookupRegression)
		if err != nil {
			return nil, err
		}
		for j, i := range plainAt {
			if j < len(pf) {
				fits[i] = pf[j]
			}
			if j < len(pe) {
				fitErrs[i] = pe[j]
			}
		}
	}
	for i, s := range specs {
		if s == nil || s.Holdout == "" {
			continue
		}
		res, fitErr, err := p.fitHoldout(ctx, s, filtered)
		if err != nil {
			return nil, err
		}
		fits[i], fitErrs[i] = res, fitErr
	}
	return regressionSlotResults(specs, fits, fitErrs)
}

// fitHoldout fits one holdout slot over its train rows and scores its
// held-out rows. The engine sees the spec without its holdout, so the
// fit — coefficients, inference, a derived resample seed — is the fit of
// the same spec over exactly the train rows. fitErr is the slot's
// data-dependent failure (the fit's own, or model.HoldoutEmpty); err a
// fatal fault.
func (p *Processor) fitHoldout(ctx context.Context, spec *types.RegressionSpec, filtered []*Record) (res *types.RegressionResult, fitErr, err error) {
	prog, err := p.holdoutProgram(spec.Holdout)
	if err != nil {
		return nil, nil, err
	}
	var train, held []regression.Record
	nNull := 0
	for _, rec := range filtered {
		in, known, err := holdoutVerdict(prog, rec)
		if err != nil {
			return nil, nil, err
		}
		switch {
		case !known:
			nNull++
		case in:
			held = append(held, rec)
		default:
			train = append(train, rec)
		}
	}
	fitSpec := *spec
	fitSpec.Holdout = ""
	fits, fitErrs, err := regression.FitEachBufferedWithContext(ctx, []*types.RegressionSpec{&fitSpec}, p.schema, train, p.exts.LookupRegression)
	if err != nil {
		return nil, nil, err
	}
	if len(fitErrs) > 0 && fitErrs[0] != nil {
		return nil, fitErrs[0], nil
	}
	if len(fits) == 0 || fits[0] == nil {
		return nil, nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_INTERNAL,
			"regression holdout fit produced no result", map[string]any{"regression": string(spec.Type)})
	}
	res = fits[0]
	name := spec.Name
	if name == "" {
		name = string(spec.Type)
	}
	b, err := model.Bind(name, &fitSpec, res)
	if err != nil {
		return nil, nil, err
	}
	scorer, err := b.Scorer(p.exts.RegressionScorer, model.ScoreParams{Fit: name})
	if err != nil {
		return nil, nil, err
	}
	figures, ferr := regression.HoldoutFigures(spec, scorer, held, nNull)
	if ferr != nil {
		return nil, ferr, nil
	}
	res.Holdout = figures
	return res, nil, nil
}

// holdoutProgram compiles a holdout predicate the way an expression
// filter's build does (total() and coef() refusals, then the boolean
// compile at exprenv.SiteHoldout), against the run's schema, derived
// key view, named vectors and bound models.
func (p *Processor) holdoutProgram(src string) (*exprProgram, error) {
	if cerr := totalcall.Refusal(src, exprenv.SiteHoldout); cerr != nil {
		return nil, cerr
	}
	if err := p.exts.coefStaticRefusal(src, p.models); err != nil {
		return nil, err
	}
	if err := p.exts.coefFailedRefusal(src, p.models); err != nil {
		return nil, err
	}
	return newExprProgramKeyed(src, exprenv.SiteHoldout, p.schema, p.keySchema, p.exprVectors, p.exts.referenceTables(), p.exts.exprOptionsWithModels(p.models, nil))
}

// holdoutVerdict evaluates the predicate on rec: (in, true) for a
// boolean verdict, known=false for a null one — a null operand that made
// it unknown, or a nil result.
func holdoutVerdict(prog *exprProgram, rec *Record) (in, known bool, err error) {
	out, nullInput, err := prog.runRecord(rec)
	if err != nil {
		if nullInput && exprRefusal(err) == nil {
			return false, false, nil
		}
		return false, false, err
	}
	if out == nil {
		return false, false, nil
	}
	b, ok := out.(bool)
	if !ok {
		if _, vec := out.([]float64); vec {
			return false, false, vectorResultError("regression holdout")
		}
		return false, false, errors.NewCodedError(errors.PROCESSING_RUNTIME,
			fmt.Sprintf("regression holdout expression must return bool, got %T", out))
	}
	return b, true, nil
}
