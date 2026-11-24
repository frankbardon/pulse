package types

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
)

// CovarianceType names the coefficient covariance estimator a regression
// reports its standard errors from (RegressionSpec.Covariance.Type).
type CovarianceType string

const (
	// CovarianceClassical is the model-based covariance every fit
	// reports without a Covariance block: σ̂²(XᵀX)⁻¹ on REG_OLS,
	// (XᵀWX)⁻¹ on REG_GLM, the NIG posterior on REG_BAYES_LINEAR.
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
	// Cluster field's levels, G/(G − 1)·(N* − 1)/(N* − k); REG_OLS reads
	// it on G − 1 df, REG_GLM and REG_BAYES_LINEAR stay normal. With
	// Cluster2 it is the Cameron–Gelbach–Miller two-way sandwich
	// V₁ + V₂ − V₁∩₂, each term with its own G/(G − 1); REG_OLS reads it
	// on min(G₁, G₂) − 1 df.
	CovarianceCluster CovarianceType = "cluster"
	// CovarianceHAC is Newey–West: a Bartlett-kernel sandwich over the
	// rows ordered by OrderBy, truncated at Lags, scaled N*/(N* − k).
	CovarianceHAC CovarianceType = "hac"
	// CovarianceDriscollKraay is Driscoll–Kraay (1998): the scores of
	// every row in one OrderBy period are summed (across entities)
	// before a Bartlett-kernel sandwich over the periods, truncated at
	// Lags, scaled N*/(N* − k) — robust to cross-sectional and serial
	// dependence in a panel.
	CovarianceDriscollKraay CovarianceType = "driscoll_kraay"
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
		CovarianceDriscollKraay,
	}
}

// RegressionCovariance selects the coefficient covariance estimator of a
// regression (RegressionSpec.Covariance) on REG_OLS, REG_GLM (the
// sandwich over the IRLS scores) or REG_BAYES_LINEAR (Müller's sandwich
// posterior, which REPLACES the posterior covariance and keeps the
// posterior mean). The estimator drives the standard errors, the
// p-values, the confidence (and credible) intervals and, when
// RegressionSpec.Vcov is set, the emitted Vcov — so √diag(Vcov) equals
// StdErrors under every robust type. Absent, or Type "classical", is the
// model-based covariance, byte-identical to a request without the block.
//
// Any other type needs per-row residuals (and, for hc2 / hc3, leverage),
// so it forces the buffered path: a second pass over the fitted rows.
// A null Cluster or OrderBy value drops the row from the fit, like a
// null predictor.
type RegressionCovariance struct {
	// Type is the estimator: classical, hc0, hc1, hc2, hc3, cluster, hac
	// or driscoll_kraay. Required.
	Type CovarianceType `json:"type"`

	// Cluster names the field whose levels are the clusters (type
	// cluster only; a categorical or unsigned-integer field). Fewer than
	// two clusters is refused.
	Cluster string `json:"cluster,omitempty"`

	// Cluster2 names a second cluster field (type cluster only, beside
	// Cluster; the same field types): two-way clustering, Cameron,
	// Gelbach & Miller (2011) — V₁ + V₂ − V₁∩₂, the one-way sandwiches
	// over each field's levels less the one over their intersection
	// cells, each scaled by its own G/(G − 1) and all by
	// (N* − 1)/(N* − k) (R sandwich::vcovCL(cluster = ~a + b), type
	// HC1, multi0 FALSE). REG_OLS, REG_GLM and REG_BAYES_LINEAR only.
	// Fewer than two levels on either field is refused; naming Cluster
	// again is a configuration fault.
	Cluster2 string `json:"cluster2,omitempty"`

	// OrderBy names the field that orders the series (types hac and
	// driscoll_kraay, required by both; a date, datetime or numeric
	// field). Under hac without Entity each row is one period: two rows
	// with the same value are refused. With Entity (or under
	// driscoll_kraay) the distinct values are the panel's periods, and
	// a lag counts periods, not rows.
	OrderBy string `json:"order_by,omitempty"`

	// Entity names the field whose levels are the panel's entities
	// (types hac and driscoll_kraay only; a categorical, unsigned
	// integer, date, datetime or packed_bool field — the
	// order.partition key types). Under hac it makes the estimator
	// per-entity Newey–West: each entity's rows are one series in
	// OrderBy order and the lagged products never cross entities.
	// Under driscoll_kraay it is optional and only asserts the panel
	// shape. Either way an entity repeating an OrderBy value is
	// refused; other entities may share it.
	Entity string `json:"entity,omitempty"`

	// Lags is the Bartlett truncation lag (types hac and driscoll_kraay
	// only): a non-negative integer, or "auto" (also the meaning when
	// absent) for ⌊4·(n/100)^(2/9)⌋ — n the fit's rows under a plain
	// hac, its distinct OrderBy periods with Entity or under
	// driscoll_kraay.
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
// rows: ⌊4·(n/100)^(2/9)⌋ (Newey & West 1994's rule of thumb), computed
// exactly in integers. For n > 0 and an integer L ≥ 0,
//
//	L ≤ 4·(n/100)^(2/9)  ⇔  (L/4)^9 ≤ (n/100)^2  ⇔  100²·L^9 ≤ 4^9·n²
//	                     ⇔  625·L^9 ≤ 2^14·n²      (both sides ÷ 16)
//
// so the lag is the largest L satisfying the last inequality, found by
// binary search on math/big integers. A floating-point power is off by
// one wherever 4·(n/100)^(2/9) is an exact integer and the power rounds
// below it — n = 100·m^9, lag 4·m²: math.Pow gives 35 at n = 1968300
// (m = 3), where the true lag is 36. types is a contracts leaf, so this
// stays on the stdlib (math/big), not linalg/detmath.
func AutoHACLags(n int) int {
	if n <= 0 {
		return 0
	}
	// rhs = 2^14·n².
	rhs := new(big.Int).SetInt64(int64(n))
	rhs.Mul(rhs, rhs)
	rhs.Lsh(rhs, 14)
	fits := func(l int64) bool { // 625·l^9 ≤ rhs
		lhs := new(big.Int).Exp(big.NewInt(l), big.NewInt(9), nil)
		lhs.Mul(lhs, big.NewInt(625))
		return lhs.Cmp(rhs) <= 0
	}
	// fits(0) always holds; fits(1<<15) never does for an int64 n
	// (625·2^135 > 2^14·2^126). Invariant: fits(lo) && !fits(hi).
	lo, hi := int64(0), int64(1)<<15
	for hi-lo > 1 {
		mid := lo + (hi-lo)/2
		if fits(mid) {
			lo = mid
		} else {
			hi = mid
		}
	}
	return int(lo)
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
	// (type cluster); a REG_OLS DF is then G − 1.
	Cluster  string `json:"cluster,omitempty"`
	Clusters int    `json:"clusters,omitempty"`

	// Cluster2 and Clusters2 are the second cluster field and its level
	// count G₂ (two-way cluster); a REG_OLS DF is then
	// min(G₁, G₂) − 1. Absent on a one-way cluster fit.
	Cluster2  string `json:"cluster2,omitempty"`
	Clusters2 int    `json:"clusters2,omitempty"`

	// OrderBy and Lags are the series order field and the truncation lag
	// the fit used, "auto" resolved (types hac and driscoll_kraay).
	OrderBy string `json:"order_by,omitempty"`
	Lags    *int   `json:"lags,omitempty"`

	// Entity and Entities are the panel's entity field and its level
	// count in the fitted rows (hac or driscoll_kraay with an entity).
	Entity   string `json:"entity,omitempty"`
	Entities int    `json:"entities,omitempty"`

	// Periods is the number of distinct OrderBy values in the fitted
	// rows — the series length a lag counts in (hac with an entity,
	// driscoll_kraay); absent on a plain hac, whose periods are its
	// rows.
	Periods int `json:"periods,omitempty"`
}
