package regression

import (
	"math"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/reglink"
	"github.com/frankbardon/pulse/internal/statdist"
	"github.com/frankbardon/pulse/linalg/detmath"
)

// glmFamily bundles the link function, inverse link, link derivative,
// variance function, and deviance contribution for a single GLM error
// family + link combination. The IRLS loop in glm_irls.go consumes this
// interface without branching on the family name; the families table
// (selectFamily) wires concrete implementations for every pair
// internal/reglink lists: binomial logit / probit / cloglog, poisson
// log / identity / sqrt, gamma inverse / log / identity.
//
// All methods operate scalar-wise. Numerical care:
//
//   - logit / log links can saturate; we clamp mu away from the
//     boundaries (0/1 for binomial; > eps for poisson/gamma) before
//     evaluating the link so the working response stays finite.
//   - The non-canonical links are R's (stats 4.6 make.link): probit
//     and cloglog clamp η / μ and floor dμ/dη at machine epsilon as R
//     does, and an identity / sqrt link whose μ must stay positive
//     reports η ≤ 0 invalid (ValidEta), so the IRLS loop halves the
//     step there as glm.fit does.
//   - The deviance term y·log(y/mu) uses the convention 0·log0 = 0.
//
// Nothing downstream may assume a canonical link: the score is
// x·(y − μ)·μ'(η)/V(μ) and the working weight μ'(η)²/V(μ) under every
// link (on a canonical one they reduce to x·(y − μ) and V(μ)), so the
// IRLS system, the covariance, the sandwich, the leverage and the
// residuals all read DmuDeta and Variance, never the shortcut.
type glmFamily struct {
	// Name and link echo the resolved family / link strings so the
	// engine can populate RegressionResult.Family / .Link without
	// re-parsing the spec.
	Name string
	Link string

	// Canonical marks the family's canonical link (binomial logit,
	// poisson log, gamma inverse) — the pre-U65 links, whose IRLS path
	// is kept byte-identical (see irlsFit's first-step restart).
	Canonical bool

	// LinkFunc returns g(mu) on the linear-predictor scale (η).
	LinkFunc func(mu float64) float64

	// InverseLink returns g⁻¹(η) on the response scale (μ).
	InverseLink func(eta float64) float64

	// DmuDeta returns ∂μ/∂η evaluated at the current η.
	DmuDeta func(eta float64) float64

	// Variance returns V(μ) — the variance function of the family.
	Variance func(mu float64) float64

	// Deviance returns the per-row deviance contribution. Sum over rows
	// times 2 gives the model deviance. 0·log0 is handled internally.
	Deviance func(y, mu float64) float64

	// SafeMean returns a mu0 suitable for η0 = g(mu0) when ȳ falls on
	// the link's saturation boundary (e.g. ȳ = 0 for binomial logit).
	// Implementations clamp the input to a family-specific interior.
	SafeMean func(yBar float64) float64

	// MuStart is the per-row starting mean μ⁰ for a response y under
	// prior weight w — R's family `initialize` (binomial
	// (w·y + 0.5)/(w + 1), poisson y + 0.1, gamma y); the IRLS loop
	// starts from η⁰ = LinkFunc(μ⁰).
	MuStart func(y, w float64) float64

	// ValidEta, when set, reports whether η is inside the link's valid
	// range (R's family valideta / validmu); a step landing outside it
	// is halved. nil = every finite η is valid.
	ValidEta func(eta float64) bool

	// EstimatesDispersion reports that the family's dispersion φ is a
	// free parameter (gamma): the classical standard errors, vcov,
	// p-values and intervals read φ̂ = Pearson χ² / (N* − k) and a
	// Student t on N* − k, as R's summary.glm does. false (binomial,
	// poisson) fixes φ = 1 by the family assumption: Wald z. It is also
	// selection's family hook: a candidate is scored by the gamma
	// log-likelihood at φ = deviance / N* with φ counted as a parameter
	// (aicBICFromGamma, R's AIC(glm)), not deviance + 2k. A new family
	// that sets it must give aicBICFromGamma's place its own density.
	EstimatesDispersion bool
}

// log1mMu is log(1 − μ) for a probability μ: log1p(−μ) where that is
// the accurate form, and 1 − μ floored at the smallest positive float
// when μ rounds to 1.
func log1mMu(mu float64) float64 {
	if mu < 0.5 {
		return detmath.Log1p(-mu)
	}
	return detmath.Log(math.Max(1-mu, math.SmallestNonzeroFloat64))
}

// epsilons used for the link and starting-value clamps (LinkFunc,
// SafeMean) — never the variance or deviance, which follow R unclamped.
// binomialEps is also the separation boundary (separationMuEps).
const (
	binomialEps = 1e-9
	poissonEps  = 1e-9
	gammaEps    = 1e-9
)

// binomialLogit is the Family("binomial"), Link("logit") combination
// that produces logistic regression. μ ∈ (0, 1).
var binomialLogit = glmFamily{
	Name:      "binomial",
	Link:      "logit",
	Canonical: true,
	LinkFunc: func(mu float64) float64 {
		if mu < binomialEps {
			mu = binomialEps
		} else if mu > 1-binomialEps {
			mu = 1 - binomialEps
		}
		return detmath.Log(mu / (1 - mu))
	},
	InverseLink: func(eta float64) float64 {
		// 1 / (1 + e^{-η}). Branch on η to keep both tails numerically
		// stable: for very negative η, e^η is small and we use that
		// form directly; for very positive η, e^{-η} is small.
		if eta >= 0 {
			z := statdist.ExpAMD64FMA(-eta)
			return 1.0 / (1.0 + z)
		}
		z := statdist.ExpAMD64FMA(eta)
		return z / (1.0 + z)
	},
	DmuDeta: func(eta float64) float64 {
		// μ(1−μ). Compute μ directly to share the InverseLink stability
		// trick (avoiding the eta → ±∞ overflow path).
		var mu float64
		if eta >= 0 {
			z := statdist.ExpAMD64FMA(-eta)
			mu = 1.0 / (1.0 + z)
		} else {
			z := statdist.ExpAMD64FMA(eta)
			mu = z / (1.0 + z)
		}
		return mu * (1 - mu)
	},
	Variance: func(mu float64) float64 {
		// μ(1−μ), unclamped (R's binomial variance): the working weight
		// μ'(η)²/V(μ) is then exactly μ(1−μ) — DmuDeta computes the
		// same product — however close a fitted probability sits to 0
		// or 1. (A clamp here once floored V at 1e-9 and so shrank the
		// working weight of every row fitted beyond 1e-9, moving the
		// converged coefficients off R's.) μ exactly 0 or 1 (|η| past
		// float range) gives V = 0 and a zero working weight.
		return mu * (1 - mu)
	},
	Deviance: func(y, mu float64) float64 {
		// 2·[y·log(y/μ) + (1−y)·log((1−y)/(1−μ))]; the factor 2 is
		// applied by the caller after summing per-row contributions.
		// Unclamped but for μ (or 1 − μ) rounding to exactly 0, floored
		// at the smallest positive float so the term stays finite.
		var a, b float64
		if y > 0 {
			a = y * detmath.Log(y/math.Max(mu, math.SmallestNonzeroFloat64))
		}
		if y < 1 {
			b = (1 - y) * (detmath.Log(1-y) - log1mMu(mu))
		}
		return a + b
	},
	SafeMean: func(yBar float64) float64 {
		if yBar < binomialEps {
			return binomialEps
		}
		if yBar > 1-binomialEps {
			return 1 - binomialEps
		}
		return yBar
	},
	MuStart: func(y, w float64) float64 {
		return (float64(w*y) + 0.5) / (w + 1)
	},
}

// poissonLog is the Family("poisson"), Link("log") combination —
// Poisson regression with the canonical log link. μ > 0.
var poissonLog = glmFamily{
	Name:      "poisson",
	Link:      "log",
	Canonical: true,
	LinkFunc: func(mu float64) float64 {
		if mu < poissonEps {
			mu = poissonEps
		}
		return detmath.Log(mu)
	},
	InverseLink: func(eta float64) float64 {
		return statdist.ExpAMD64FMA(eta)
	},
	DmuDeta: func(eta float64) float64 {
		return statdist.ExpAMD64FMA(eta)
	},
	Variance: func(mu float64) float64 {
		// μ, unclamped (R's poisson variance): the working weight
		// μ'(η)²/V(μ) is exactly μ for every fitted mean.
		return mu
	},
	Deviance: func(y, mu float64) float64 {
		// 2·[y·log(y/μ) − (y − μ)]; factor 2 applied by caller.
		// Unclamped but for μ underflowing to 0.
		var a float64
		if y > 0 {
			a = y * detmath.Log(y/math.Max(mu, math.SmallestNonzeroFloat64))
		}
		return a - (y - mu)
	},
	SafeMean: func(yBar float64) float64 {
		if yBar < poissonEps {
			return poissonEps
		}
		return yBar
	},
	MuStart: func(y, _ float64) float64 {
		return y + 0.1
	},
}

// gammaInverse is the Family("gamma"), Link("inverse") combination —
// the canonical link for the gamma family. μ > 0. Its dispersion is
// estimated (EstimatesDispersion); pinned to R glm(family = Gamma()) by
// glm_irls.json / glm_gamma.json.
var gammaInverse = glmFamily{
	Name:                "gamma",
	Link:                "inverse",
	Canonical:           true,
	EstimatesDispersion: true,
	LinkFunc: func(mu float64) float64 {
		if mu < gammaEps {
			mu = gammaEps
		}
		return 1.0 / mu
	},
	InverseLink: func(eta float64) float64 {
		// 1/η. Guard against η = 0; the IRLS loop should not produce
		// that under a non-pathological initialisation but the guard
		// keeps the function total for callers.
		if math.Abs(eta) < gammaEps {
			if eta >= 0 {
				eta = gammaEps
			} else {
				eta = -gammaEps
			}
		}
		return 1.0 / eta
	},
	DmuDeta: func(eta float64) float64 {
		// d(1/η)/dη = −1/η² = −μ².
		if math.Abs(eta) < gammaEps {
			if eta >= 0 {
				eta = gammaEps
			} else {
				eta = -gammaEps
			}
		}
		mu := 1.0 / eta
		return -mu * mu
	},
	Variance: func(mu float64) float64 {
		if mu < gammaEps {
			mu = gammaEps
		}
		return mu * mu
	},
	Deviance: func(y, mu float64) float64 {
		// 2·[(y − μ)/μ − log(y/μ)]; factor 2 applied by caller.
		if mu < gammaEps {
			mu = gammaEps
		}
		if y < gammaEps {
			y = gammaEps
		}
		return (y-mu)/mu - detmath.Log(y/mu)
	},
	SafeMean: func(yBar float64) float64 {
		if yBar < gammaEps {
			return gammaEps
		}
		return yBar
	},
	MuStart: func(y, _ float64) float64 {
		return y
	},
	// μ = 1/η must stay positive (R's Gamma validmu).
	ValidEta: func(eta float64) bool {
		return eta > 0
	},
}

const (
	// machineEps is R's .Machine$double.eps, the floor make.link puts
	// under the probit / cloglog / log μ'(η) and μ.
	machineEps = 2.220446049250313e-16
	// probitThresh is −qnorm(machineEps): R's probit linkinv clamps η
	// to ±probitThresh, so μ stays inside [ε, 1 − ε].
	probitThresh = 8.125890664701906
)

// clampProb keeps a starting probability off 0 and 1 (the logit link's
// clamp) so g(μ) stays finite.
func clampProb(mu float64) float64 {
	if mu < binomialEps {
		return binomialEps
	}
	if mu > 1-binomialEps {
		return 1 - binomialEps
	}
	return mu
}

// positiveEta is the ValidEta of a link whose μ must stay positive and
// whose η is μ (identity) or √μ (sqrt): R's poisson / Gamma validmu
// (identity) and the sqrt link's valideta.
func positiveEta(eta float64) bool { return eta > 0 }

// withLink is base (a canonical family) on a non-canonical link: the
// family's variance, deviance, starting means and dispersion rule, the
// link's own g, g⁻¹, μ'(η) and valid range (nil: every finite η).
func withLink(base glmFamily, link string, g, inv, dmu func(float64) float64, valid func(float64) bool) glmFamily {
	f := base
	f.Link, f.Canonical = link, false
	f.LinkFunc, f.InverseLink, f.DmuDeta, f.ValidEta = g, inv, dmu, valid
	return f
}

// binomialProbit: μ = Φ(η), R's binomial(link = "probit").
var binomialProbit = withLink(binomialLogit, "probit",
	func(mu float64) float64 { return statdist.NormalQuantile(clampProb(mu)) },
	func(eta float64) float64 {
		eta = math.Max(math.Min(eta, probitThresh), -probitThresh)
		return float64(0.5 * detmath.Erfc(-eta/math.Sqrt2))
	},
	func(eta float64) float64 {
		// φ(η) = e^{−η²/2}/√(2π), floored at ε.
		return math.Max(detmath.Exp(float64(-0.5*eta)*eta)/math.Sqrt(2*math.Pi), machineEps)
	},
	nil,
)

// binomialCloglog: μ = 1 − exp(−e^η), R's binomial(link = "cloglog").
var binomialCloglog = withLink(binomialLogit, "cloglog",
	func(mu float64) float64 { return detmath.Log(-log1mMu(clampProb(mu))) },
	func(eta float64) float64 {
		return math.Max(math.Min(-detmath.Expm1(-detmath.Exp(eta)), 1-machineEps), machineEps)
	},
	func(eta float64) float64 {
		e := detmath.Exp(math.Min(eta, 700))
		return math.Max(float64(e*detmath.Exp(-e)), machineEps)
	},
	nil,
)

// poissonIdentity: μ = η, R's poisson(link = "identity"); μ must stay
// positive.
var poissonIdentity = withLink(poissonLog, "identity",
	func(mu float64) float64 { return mu },
	func(eta float64) float64 { return eta },
	func(float64) float64 { return 1 },
	positiveEta,
)

// poissonSqrt: μ = η², R's poisson(link = "sqrt"); η must stay
// positive (its valideta).
var poissonSqrt = withLink(poissonLog, "sqrt",
	func(mu float64) float64 { return math.Sqrt(math.Max(mu, 0)) },
	func(eta float64) float64 { return float64(eta * eta) },
	func(eta float64) float64 { return 2 * eta },
	positiveEta,
)

// gammaLog: μ = e^η floored at ε, R's Gamma(link = "log").
var gammaLog = withLink(gammaInverse, "log",
	func(mu float64) float64 { return detmath.Log(math.Max(mu, gammaEps)) },
	func(eta float64) float64 { return math.Max(detmath.Exp(eta), machineEps) },
	func(eta float64) float64 { return math.Max(detmath.Exp(eta), machineEps) },
	nil,
)

// gammaIdentity: μ = η, R's Gamma(link = "identity"); μ must stay
// positive.
var gammaIdentity = withLink(gammaInverse, "identity",
	func(mu float64) float64 { return mu },
	func(eta float64) float64 { return eta },
	func(float64) float64 { return 1 },
	positiveEta,
)

// glmFamilies is every (family, link) pair the engine fits, keyed
// family then link — exactly internal/reglink's table
// (TestSelectFamily_MatchesRegLink).
var glmFamilies = map[string]map[string]glmFamily{
	"binomial": {"logit": binomialLogit, "probit": binomialProbit, "cloglog": binomialCloglog},
	"poisson":  {"log": poissonLog, "identity": poissonIdentity, "sqrt": poissonSqrt},
	"gamma":    {"inverse": gammaInverse, "log": gammaLog, "identity": gammaIdentity},
}

// selectFamily resolves a (family, link) pair to a glmFamily. Empty
// Link picks the family's canonical default (binomial → logit, poisson
// → log, gamma → inverse). An unknown family is
// PROCESSING_REGRESSION_INVALID_FAMILY; a link outside the family's set
// is PROCESSING_REGRESSION_INVALID_LINK (internal/reglink, the rule
// predict applies too).
func selectFamily(family, link string) (glmFamily, error) {
	links, ok := glmFamilies[family]
	if !ok {
		return glmFamily{}, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_INVALID_FAMILY,
			"REG_GLM Family must be one of binomial, poisson, gamma; got "+family,
			map[string]any{"family": family, "allowed": []string{"binomial", "poisson", "gamma"}},
		)
	}
	if cerr := reglink.CheckPair(family, link); cerr != nil {
		return glmFamily{}, cerr
	}
	return links[reglink.Resolve(family, link)], nil
}
