package regression

// credibleInterval returns the symmetric [lower, upper] interval for a
// Student-t marginal posterior with `mean`, std error `se`, and
// `df = 2·a_n` degrees of freedom. `level` is the credible-interval
// mass (e.g., 0.95 for a 95% interval).
//
// Algorithm: the (1−α/2) quantile of t(df) — call it t_q — sets the
// half-width. The interval is mean ± t_q · se.
//
// REG_BAYES_LINEAR uses this for every coefficient (intercept included).
// The actual quantile evaluation lives in studentTQuantile (stats.go);
// this helper exists so the call site reads as a single line and the
// degenerate-input handling lives in one place.
func credibleInterval(mean, se, df, level float64) [2]float64 {
	if se == 0 {
		return [2]float64{mean, mean}
	}
	q := studentTQuantile(0.5+level/2, df)
	half := q * se
	return [2]float64{mean - half, mean + half}
}
