package model

import (
	"context"
	"testing"
)

// TestRegressionLabel (#787): a regression's own name wins; an unnamed
// one under a Compose fit takes the fit's name; anything else stays "".
func TestRegressionLabel(t *testing.T) {
	t.Parallel()

	fit := WithFitName(context.Background(), "mmm")
	for _, c := range []struct {
		ctx        context.Context
		name, want string
	}{
		{fit, "", "mmm"},
		{fit, "own", "own"},
		{context.Background(), "", ""},
		{WithFitName(context.Background(), ""), "", ""},
	} {
		if got := RegressionLabel(c.ctx, c.name); got != c.want {
			t.Errorf("RegressionLabel(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}
