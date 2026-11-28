package model

import "context"

// fitNameKey carries the Compose fit a regression runs under.
type fitNameKey struct{}

// WithFitName marks ctx as running Compose fit name's one regression,
// so a model that labels its runtime messages by regression name can
// fall back to the fit's name when the regression has none (#787).
func WithFitName(ctx context.Context, name string) context.Context {
	if name == "" {
		return ctx
	}
	return context.WithValue(ctx, fitNameKey{}, name)
}

// RegressionLabel is the name a runtime message calls a regression:
// its own name, else the Compose fit's it runs under (WithFitName),
// else "". Only message text reads it — never a seed, a details key or
// any figure, so naming a fit changes no number.
func RegressionLabel(ctx context.Context, name string) string {
	if name != "" || ctx == nil {
		return name
	}
	fit, _ := ctx.Value(fitNameKey{}).(string)
	return fit
}
