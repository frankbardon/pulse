package regression

import (
	"math"
	"slices"
	"strconv"

	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/linalg/detmath"
	"github.com/frankbardon/pulse/types"
)

// HoldoutFigures scores rows — the records spec's holdout predicate
// held out of the fit — with scorer (the fitted model's RowScorer, on
// the response scale) and folds the out-of-sample figures of
// types.RegressionHoldout; nNull is the count of rows whose predicate
// was null, echoed as NNull.
//
// A row is scored when its target (InputValue) and score are present
// and finite and, on a weighted slot (spec is the STAMPED spec, as the
// fit reads it), its weight is valid and positive — the fit's own row
// rule (rowWeights.of). N counts those rows raw. The losses fold in
// record order exactly as AGG_RMSE / AGG_MAE fold them (Σw·e², Σw·|e|,
// Σw; e = y − ŷ), so the figures equal the ATTR_REG_PREDICT + AGG_*
// route's; R2 is 1 − Σw·e² / Σw·(y − ȳ)², ȳ the held-out rows' own
// weighted mean, NaN (null on the wire) when Σw·(y − ȳ)² is 0. No scored
// row is the slot's data-dependent failure (model.HoldoutEmpty).
func HoldoutFigures(spec *types.RegressionSpec, scorer model.RowScorer, rows []Record, nNull int) (*types.RegressionHoldout, error) {
	rw := newRowWeights(spec)
	var (
		n                         int
		lossSq, lossAbs, sumW, wY float64
		ys, ws                    []float64
	)
	for _, rec := range rows {
		y, ok := InputValue(rec, spec.Target)
		if !ok {
			continue
		}
		yhat, ok := scorer.Score(rec)
		if !ok || math.IsNaN(yhat) || math.IsInf(yhat, 0) {
			continue
		}
		w, ok := rw.of(rec)
		if !ok {
			continue
		}
		d := y - yhat
		sq := d * d
		lossSq += float64(w * sq)
		lossAbs += float64(w * math.Abs(d))
		sumW += w
		wY += float64(w * y)
		ys = append(ys, y)
		ws = append(ws, w)
		n++
	}
	if n == 0 {
		return nil, model.HoldoutEmpty(spec)
	}
	mean := wY / sumW
	var sst float64
	for i, y := range ys {
		c := y - mean
		sst += float64(ws[i] * (c * c))
	}
	r2 := math.NaN()
	if sst > 0 {
		r2 = 1 - lossSq/sst
	}
	return &types.RegressionHoldout{
		N:     n,
		NNull: nNull,
		RMSE:  new(math.Sqrt(lossSq / sumW)),
		MAE:   new(lossAbs / sumW),
		R2:    new(r2),
	}, nil
}

// HoldoutOutcomeFigures is HoldoutFigures for a categorical-outcome fit
// (model.OutcomeType: REG_ORDINAL, REG_MULTINOMIAL): it scores rows with
// ps, the fit's full per-row probability vector over its outcomes, and
// folds the proper scores of types.RegressionHoldout — log_loss, brier,
// accuracy and, on an ordered outcome (ordered: REG_ORDINAL), rps.
//
// A row is scored when its target is present (InputValue) and its
// outcome — the categorical label, else the value formatted as
// REG_ORDINAL's levels are (strconv 'g', -1) — is one of ps.Outcomes(),
// every probability is finite, and its weight passes the fit's own row
// rule (rowWeights.of). A held-out outcome the fit does not carry (a
// level outside params.levels, a category no train row had) has no
// probability, so its row is not scored. Each figure is the weighted
// mean over the scored rows, folded in record order: −ln p_y (natural
// log, +Inf → null when p_y is 0), Σ_k (p_k − o_k)², [argmax p = y] (the
// first outcome on a tie, as the class output) and Σ_{k<K} (F_k −
// O_k)²/(K − 1) over the cumulative F_k and O_k = [y ≤ k]. A target
// label the dictionary cannot decode is the record's ENCODING_INVALID,
// returned as the slot's failure; no scored row is model.HoldoutEmpty.
func HoldoutOutcomeFigures(spec *types.RegressionSpec, ps model.ProbabilityScorer, ordered bool, rows []Record, nNull int) (*types.RegressionHoldout, error) {
	rw := newRowWeights(spec)
	outcomes := ps.Outcomes()
	k := len(outcomes)
	p := make([]float64, k)
	var (
		n                                    int
		sumW, logLoss, brier, correct, rpsum float64
	)
	for _, rec := range rows {
		y, ok, err := holdoutOutcome(rec, spec.Target, outcomes)
		if err != nil {
			return nil, err
		}
		if !ok || !ps.Probabilities(rec, p) || !finiteAll(p) {
			continue
		}
		w, ok := rw.of(rec)
		if !ok {
			continue
		}
		var sq, best float64
		arg := 0
		for j, pj := range p {
			d := pj
			if j == y {
				d = pj - 1
			}
			sq += float64(d * d)
			if j == 0 || pj > best {
				best, arg = pj, j
			}
		}
		logLoss += float64(w * -detmath.Log(p[y]))
		brier += float64(w * sq)
		if arg == y {
			correct += w
		}
		if ordered && k > 1 {
			var cum, rs float64
			for j := 0; j < k-1; j++ {
				cum += p[j]
				d := cum
				if y <= j {
					d = cum - 1
				}
				rs += float64(d * d)
			}
			rpsum += float64(w * (rs / float64(k-1)))
		}
		sumW += w
		n++
	}
	if n == 0 {
		return nil, model.HoldoutEmpty(spec)
	}
	h := &types.RegressionHoldout{
		N:        n,
		NNull:    nNull,
		LogLoss:  new(logLoss / sumW),
		Brier:    new(brier / sumW),
		Accuracy: new(correct / sumW),
	}
	if ordered {
		h.RPS = new(rpsum / sumW)
	}
	return h, nil
}

// holdoutOutcome is rec's observed outcome as its position among
// outcomes: the target's categorical label, else its value formatted as
// REG_ORDINAL formats a numeric level; ok=false when the target is
// null, absent, non-finite or not one of outcomes. A categorical code
// the dictionary cannot decode is the record's error.
func holdoutOutcome(rec Record, target string, outcomes []string) (int, bool, error) {
	v, ok := InputValue(rec, target)
	if !ok {
		return 0, false, nil
	}
	label := strconv.FormatFloat(v, 'g', -1, 64)
	if lr, isLabeled := rec.(LabeledRecord); isLabeled {
		s, cat, err := lr.StringValueChecked(target)
		if err != nil {
			return 0, false, err
		}
		if cat {
			label = s
		}
	}
	i := slices.Index(outcomes, label)
	return i, i >= 0, nil
}

// finiteAll reports whether every value is finite.
func finiteAll(v []float64) bool {
	for _, x := range v {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return false
		}
	}
	return true
}
