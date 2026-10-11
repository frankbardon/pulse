package types

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
)

// CovarianceType names the coefficient covariance estimator a regression
// reports its standard errors from (RegressionSpec.Covariance.Type).
type CovarianceType string

const (
	// CovarianceClassical is the model-based covariance every fit
	// reports without a Covariance block: σ̂²(XᵀX)⁻¹ on REG_OLS.
	CovarianceClassical CovarianceType = "classical"
	// CovarianceHC0 is White's heteroskedasticity-consistent sandwich,
	// meat Σ e²·x·xᵀ.
	CovarianceHC0 CovarianceType = "hc0"
	// CovarianceHC1 is HC0 scaled by N*/(N* − k).
	CovarianceHC1 CovarianceType = "hc1"
	// CovarianceHC2 weights each squared residual by 1/(1 − h).
	CovarianceHC2 CovarianceType = "hc2"
	// CovarianceHC3 weights each squared residual by 1/(1 − h)².
	CovarianceHC3 CovarianceType = "hc3"
	// CovarianceCluster is the cluster-robust CR1 sandwich over the
	// Cluster field's levels, G/(G − 1)·(N* − 1)/(N* − k), on G − 1 df.
	CovarianceCluster CovarianceType = "cluster"
	// CovarianceHAC is Newey–West: a Bartlett-kernel sandwich over the
	// rows ordered by OrderBy, truncated at Lags, scaled N*/(N* − k).
	CovarianceHAC CovarianceType = "hac"
)

// AllCovarianceTypes returns every covariance estimator name in
// declaration order (classical first).
func AllCovarianceTypes() []CovarianceType {
	return []CovarianceType{
		CovarianceClassical,
		CovarianceHC0,
		CovarianceHC1,
		CovarianceHC2,
		CovarianceHC3,
		CovarianceCluster,
		CovarianceHAC,
	}
}

// RegressionCovariance selects the coefficient covariance estimator of a
// regression (RegressionSpec.Covariance). The estimator drives the
// standard errors, the p-values, the confidence intervals and, when
// RegressionSpec.Vcov is set, the emitted Vcov — so √diag(Vcov) equals
// StdErrors under every type. Absent, or Type "classical", is the
// model-based covariance, byte-identical to a request without the block.
//
// Any other type needs per-row residuals (and, for hc2 / hc3, leverage),
// so it forces the buffered path: a second pass over the fitted rows.
// A null Cluster or OrderBy value drops the row from the fit, like a
// null predictor.
type RegressionCovariance struct {
	// Type is the estimator: classical, hc0, hc1, hc2, hc3, cluster or
	// hac. Required.
	Type CovarianceType `json:"type"`

	// Cluster names the field whose levels are the clusters (type
	// cluster only; a categorical or unsigned-integer field). Fewer than
	// two clusters is refused.
	Cluster string `json:"cluster,omitempty"`

	// OrderBy names the field that orders the series (type hac only; a
	// date, datetime or numeric field). Each row is one period: two rows
	// with the same value are refused.
	OrderBy string `json:"order_by,omitempty"`

	// Lags is the Newey–West truncation lag (type hac only): a
	// non-negative integer, or "auto" (also the meaning when absent) for
	// ⌊4·(n/100)^(2/9)⌋ over the fit's n rows.
	Lags CovarianceLags `json:"lags,omitzero"`
}

// EffectiveType is the estimator c selects: classical when c is nil or
// its Type is empty.
func (c *RegressionCovariance) EffectiveType() CovarianceType {
	if c == nil || c.Type == "" {
		return CovarianceClassical
	}
	return c.Type
}

// Robust reports whether c selects a non-classical estimator — one that
// needs per-row residuals and so forces the buffered path.
func (c *RegressionCovariance) Robust() bool {
	return c.EffectiveType() != CovarianceClassical
}

// AutoHACLags is the "auto" Newey–West truncation lag for a series of n
// rows: ⌊4·(n/100)^(2/9)⌋ (Newey & West 1994's rule of thumb).
func AutoHACLags(n int) int {
	if n <= 0 {
		return 0
	}
	return int(math.Floor(4 * math.Pow(float64(n)/100, 2.0/9.0)))
}

// CovarianceLags is RegressionCovariance.Lags: absent, "auto", or a
// non-negative integer. The zero value is absent (read as "auto").
type CovarianceLags struct {
	set  bool
	auto bool
	n    int
}

// LagsAuto returns the explicit "auto" lag.
func LagsAuto() CovarianceLags { return CovarianceLags{set: true, auto: true} }

// LagsOf returns a fixed truncation lag n.
func LagsOf(n int) CovarianceLags { return CovarianceLags{set: true, n: n} }

// IsZero reports whether the lag is absent. It is what `omitzero`
// consults.
func (l CovarianceLags) IsZero() bool { return !l.set }

// IsAuto reports whether the lag is chosen by the auto rule: absent or
// "auto".
func (l CovarianceLags) IsAuto() bool { return !l.set || l.auto }

// Value returns the fixed lag and true, or (0, false) when the lag is
// absent or "auto".
func (l CovarianceLags) Value() (int, bool) {
	if !l.set || l.auto {
		return 0, false
	}
	return l.n, true
}

// MarshalJSON emits "auto" for the auto rule and the integer otherwise.
// An absent lag is never marshalled (omitzero); marshalled directly it
// reads as "auto".
func (l CovarianceLags) MarshalJSON() ([]byte, error) {
	if l.IsAuto() {
		return []byte(`"auto"`), nil
	}
	return json.Marshal(l.n)
}

// UnmarshalJSON accepts "auto" or an integer; `null` leaves the lag
// absent. Any other string, a fractional number or another JSON shape
// is an error.
func (l *CovarianceLags) UnmarshalJSON(b []byte) error {
	trimmed := bytes.TrimSpace(b)
	switch {
	case bytes.Equal(trimmed, []byte("null")):
		*l = CovarianceLags{}
		return nil
	case len(trimmed) > 0 && trimmed[0] == '"':
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return err
		}
		if s != "auto" {
			return fmt.Errorf("lags: want an integer or \"auto\", got %q", s)
		}
		*l = LagsAuto()
		return nil
	default:
		var n int
		if err := json.Unmarshal(trimmed, &n); err != nil {
			return fmt.Errorf("lags: want an integer or \"auto\": %w", err)
		}
		*l = LagsOf(n)
		return nil
	}
}

// RegressionCovarianceResult echoes the robust estimator a fit's
// standard errors came from (RegressionResult.Covariance) — absent on a
// classical fit, so a request without the block is byte-identical.
type RegressionCovarianceResult struct {
	// Type is the estimator (never classical: a classical fit carries no
	// echo).
	Type CovarianceType `json:"type"`

	// Cluster and Clusters are the cluster field and its level count G
	// (type cluster); DF is then G − 1.
	Cluster  string `json:"cluster,omitempty"`
	Clusters int    `json:"clusters,omitempty"`

	// OrderBy and Lags are the series order field and the truncation lag
	// the fit used, "auto" resolved (type hac).
	OrderBy string `json:"order_by,omitempty"`
	Lags    *int   `json:"lags,omitempty"`
}
