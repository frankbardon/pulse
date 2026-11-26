# Structural Equation Models from Go

**Audience:** embedders fitting confirmatory factor analyses or path
models (`MAT_SEM`) and reading the result as typed Go values.

## When to use it

Use `MAT_SEM` when a construct is measured by several items (a survey
battery) and you want the latent structure: loadings, factor
covariances, regressions between latents, fit indices. One fit is a
matrix in an ordinary request. Fit once and score many slots (waves,
segments, a holdout), or save the fit for later requests, through a
Compose `fits` entry. For an exploratory look at the same items, start
with `MAT_FACTOR` instead.

## One fit

```go compile
package cfa

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/types"
)

// Fit runs a two-factor CFA over the q1..q6 battery and prints its fit
// indices.
func Fit(ctx context.Context, p *pulse.Pulse) error {
	resp, err := p.Process(ctx, &pulse.Request{
		Cohort:  &types.Cohort{Filename: "survey.pulse"},
		Vectors: []types.VectorSpec{{Name: "battery", Fields: []string{"q1", "q2", "q3", "q4", "q5", "q6"}}},
		Matrices: []types.MatrixSpec{{
			Type: types.MAT_SEM, Name: "cfa", Vector: "battery",
			Params: json.RawMessage(`{"model": "service =~ q1 + q2 + q3\nvalue =~ q4 + q5 + q6"}`),
		}},
	})
	if err != nil {
		return err // a config fault: bad model syntax, unknown field
	}
	m := resp.Matrices[0]
	if m.Error != nil {
		return fmt.Errorf("fit failed on its data: %s", m.Error.Message)
	}
	fmt.Println("chi2", m.Scalars["chi2"], "cfi", m.Scalars["cfi"], "rmsea", m.Scalars["rmsea"])
	// m.Auxiliary["estimates"]: one row per parameter (lval op rval) with
	// estimate, std, SE, z and p; m.Primary: the model-implied covariance.
	return nil
}
```

A fit that fails on its data keeps its slot with `Error` set and a
`PULSE_SLOT_FAILED` warning; only a configuration fault returns an
error.

## Fits, saving and scores

In a `ComposedRequest`, a `fits` entry whose request holds one
`MAT_SEM` lands in `ComposedResponse.Fits[name].SEM`
(`types.SEMFitResult`: the model's orders and, per group, the `Lambda`,
`Beta`, `Psi`, `Theta` matrices at full precision). `FitSpec.Save`
writes it to a `.pulse-sem.json` file a later request loads through
`models`. `ATTR_SEM_SCORE` turns a fit into a per-row latent score any
slot can group, filter or regress on.

## Where the detail lives

- Model syntax, conventions (semopy and lavaan), estimators, groups,
  ordinal items and the full output:
  [Vectors and Matrices → Structural equation models](matrices.md#structural-equation-models).
- Skills: `op-mat-sem`, `sem-modeling`, `sem-fits`, `sem-groups`,
  `sem-ordinal`, `sem-comparison`, `sem-bootstrap`,
  `sem-defined-parameters` (`pulse skills show <name>`).
- Comparing nested SEM fits: [Regression Modeling](../regression.md).
- GoDoc: [`types.MatrixResult`](https://pkg.go.dev/github.com/frankbardon/pulse/types#MatrixResult),
  [`types.SEMFitResult`](https://pkg.go.dev/github.com/frankbardon/pulse/types#SEMFitResult).
