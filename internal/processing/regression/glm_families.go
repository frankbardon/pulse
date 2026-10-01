package regression

import (
	"math"

	"github.com/frankbardon/pulse/errors"
)

// glmFamily bundles the link function, inverse link, link derivative,
// variance function, and deviance contribution for a single GLM error
// family + link combination. The IRLS loop in glm.go consumes this
// interface without branching on the family name; the families table
// (selectFamily) wires concrete implementations for binomial+logit,
// poisson+log, and gamma+inverse.
//
// All methods operate scalar-wise. Numerical care:
//
//   - logit / log links can saturate; we clamp mu away from the
//     boundaries (0/1 for binomial; > eps for poisson/gamma) before
//     evaluating the link so the working response stays finite.
//   - The deviance term y·log(y/mu) uses the convention 0·log0 = 0.
type glmFamily struct {
	// Name and link echo the resolved family / link strings so the
	// engine can populate RegressionResult.Family / .Link without
	// re-parsing the spec.
	Name string
	Link string

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
}

// epsilons used for boundary clamps. Conservative enough that
// log/log1p never produce −Inf and small enough not to bias coefficients
// at typical scales.
const (
	binomialEps = 1e-9
	poissonEps  = 1e-9
	gammaEps    = 1e-9
)

// binomialLogit is the Family("binomial"), Link("logit") combination
// that produces logistic regression. μ ∈ (0, 1).
var binomialLogit = glmFamily{
	Name: "binomial",
	Link: "logit",
	LinkFunc: func(mu float64) float64 {
		if mu < binomialEps {
			mu = binomialEps
		} else if mu > 1-binomialEps {
			mu = 1 - binomialEps
		}
		return math.Log(mu / (1 - mu))
	},
	InverseLink: func(eta float64) float64 {
		// 1 / (1 + e^{-η}). Branch on η to keep both tails numerically
		// stable: for very negative η, e^η is small and we use that
		// form directly; for very positive η, e^{-η} is small.
		if eta >= 0 {
			z := math.Exp(-eta)
			return 1.0 / (1.0 + z)
		}
		z := math.Exp(eta)
		return z / (1.0 + z)
	},
	DmuDeta: func(eta float64) float64 {
		// μ(1−μ). Compute μ directly to share the InverseLink stability
		// trick (avoiding the eta → ±∞ overflow path).
		var mu float64
		if eta >= 0 {
			z := math.Exp(-eta)
			mu = 1.0 / (1.0 + z)
		} else {
			z := math.Exp(eta)
			mu = z / (1.0 + z)
		}
		return mu * (1 - mu)
	},
	Variance: func(mu float64) float64 {
		// μ(1−μ); never zero since IRLS clamps mu away from {0,1}.
		if mu < binomialEps {
			mu = binomialEps
		} else if mu > 1-binomialEps {
			mu = 1 - binomialEps
		}
		return mu * (1 - mu)
	},
	Deviance: func(y, mu float64) float64 {
		// 2·[y·log(y/μ) + (1−y)·log((1−y)/(1−μ))]; the factor 2 is
		// applied by the caller after summing per-row contributions.
		if mu < binomialEps {
			mu = binomialEps
		} else if mu > 1-binomialEps {
			mu = 1 - binomialEps
		}
		var a, b float64
		if y > 0 {
			a = y * math.Log(y/mu)
		}
		if y < 1 {
			b = (1 - y) * math.Log((1-y)/(1-mu))
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
}

// poissonLog is the Family("poisson"), Link("log") combination —
// Poisson regression with the canonical log link. μ > 0.
var poissonLog = glmFamily{
	Name: "poisson",
	Link: "log",
	LinkFunc: func(mu float64) float64 {
		if mu < poissonEps {
			mu = poissonEps
		}
		return math.Log(mu)
	},
	InverseLink: func(eta float64) float64 {
		return math.Exp(eta)
	},
	DmuDeta: func(eta float64) float64 {
		return math.Exp(eta)
	},
	Variance: func(mu float64) float64 {
		if mu < poissonEps {
			mu = poissonEps
		}
		return mu
	},
	Deviance: func(y, mu float64) float64 {
		// 2·[y·log(y/μ) − (y − μ)]; factor 2 applied by caller.
		if mu < poissonEps {
			mu = poissonEps
		}
		var a float64
		if y > 0 {
			a = y * math.Log(y/mu)
		}
		return a - (y - mu)
	},
	SafeMean: func(yBar float64) float64 {
		if yBar < poissonEps {
			return poissonEps
		}
		return yBar
	},
}

// gammaInverse is the Family("gamma"), Link("inverse") combination —
// the canonical link for the gamma family. μ > 0. Wired for completeness;
// numerical tests are deferred to a later phase per the locked plan.
var gammaInverse = glmFamily{
	Name: "gamma",
	Link: "inverse",
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
		return (y-mu)/mu - math.Log(y/mu)
	},
	SafeMean: func(yBar float64) float64 {
		if yBar < gammaEps {
			return gammaEps
		}
		return yBar
	},
}

// selectFamily resolves a (family, link) pair to a glmFamily. Empty
// Link picks the family's canonical default (binomial → logit, poisson
// → log, gamma → inverse). Unsupported (family, link) combinations
// surface as PROCESSING_REGRESSION_INVALID_FAMILY or
// PROCESSING_REGRESSION_INVALID_LINK.
//
// Reserved enum values (binomial probit/cloglog, poisson identity/sqrt,
// gamma log/identity) parse correctly at the descriptor layer but the
// engine surfaces INVALID_LINK until those links ship with their own
// numerical fixtures in a later phase.
func selectFamily(family, link string) (glmFamily, error) {
	switch family {
	case "binomial":
		switch link {
		case "", "logit":
			return binomialLogit, nil
		case "probit", "cloglog":
			return glmFamily{}, errors.NewCodedErrorWithDetails(
				errors.PROCESSING_REGRESSION_INVALID_LINK,
				"REG_GLM Family=binomial Link="+link+" is a reserved enum value but not yet implemented; use Link=logit",
				map[string]any{"family": family, "link": link, "supported": []string{"logit"}},
			)
		default:
			return glmFamily{}, errors.NewCodedErrorWithDetails(
				errors.PROCESSING_REGRESSION_INVALID_LINK,
				"REG_GLM Family=binomial requires Link in {logit, probit, cloglog}; got "+link,
				map[string]any{"family": family, "link": link, "allowed": []string{"logit", "probit", "cloglog"}},
			)
		}
	case "poisson":
		switch link {
		case "", "log":
			return poissonLog, nil
		case "identity", "sqrt":
			return glmFamily{}, errors.NewCodedErrorWithDetails(
				errors.PROCESSING_REGRESSION_INVALID_LINK,
				"REG_GLM Family=poisson Link="+link+" is a reserved enum value but not yet implemented; use Link=log",
				map[string]any{"family": family, "link": link, "supported": []string{"log"}},
			)
		default:
			return glmFamily{}, errors.NewCodedErrorWithDetails(
				errors.PROCESSING_REGRESSION_INVALID_LINK,
				"REG_GLM Family=poisson requires Link in {log, identity, sqrt}; got "+link,
				map[string]any{"family": family, "link": link, "allowed": []string{"log", "identity", "sqrt"}},
			)
		}
	case "gamma":
		switch link {
		case "", "inverse":
			return gammaInverse, nil
		case "log", "identity":
			return glmFamily{}, errors.NewCodedErrorWithDetails(
				errors.PROCESSING_REGRESSION_INVALID_LINK,
				"REG_GLM Family=gamma Link="+link+" is a reserved enum value but not yet implemented; use Link=inverse",
				map[string]any{"family": family, "link": link, "supported": []string{"inverse"}},
			)
		default:
			return glmFamily{}, errors.NewCodedErrorWithDetails(
				errors.PROCESSING_REGRESSION_INVALID_LINK,
				"REG_GLM Family=gamma requires Link in {inverse, log, identity}; got "+link,
				map[string]any{"family": family, "link": link, "allowed": []string{"inverse", "log", "identity"}},
			)
		}
	default:
		return glmFamily{}, errors.NewCodedErrorWithDetails(
			errors.PROCESSING_REGRESSION_INVALID_FAMILY,
			"REG_GLM Family must be one of binomial, poisson, gamma; got "+family,
			map[string]any{"family": family, "allowed": []string{"binomial", "poisson", "gamma"}},
		)
	}
}
