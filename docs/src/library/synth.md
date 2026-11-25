# Synthetic Data (`synth`)

**Audience:** embedders generating synthetic cohorts from Go: test
fixtures, demo data, a privacy-preserving copy of a real cohort.

## When to use which entry point

| You have | Call |
|---|---|
| A `*pulse.Pulse` | `Pulse.Synth` / `Pulse.SynthStream` / `Pulse.Profile`. They read through the instance filesystem, apply its limits and lookup tables, and write the fidelity and disclosure reports. Prefer these. |
| No instance (a fixture helper, a spec tool) | The package functions in [`synth`](https://pkg.go.dev/github.com/frankbardon/pulse/synth): `Synth` onto an `afero.Fs`, `SynthBytes` into memory, `ProfileFile` / `ProfileBytes`, `SpecFromProfile`, `ParseSpec`, `WriteSpec`. |

Both take the same `synth.Spec` and `synth.Options`. The same spec and
`Options.Seed` always produce the same bytes.

## A fixture in memory

```go compile
package fixtures

import (
	"github.com/frankbardon/pulse/synth"
)

// Scores returns a 1,000-row cohort: a uniform u16 score and a
// weighted region.
func Scores() ([]byte, error) {
	spec := &synth.Spec{RowCount: 1000, Fields: []synth.FieldSpec{
		{Name: "score", Type: "u16", Distribution: "uniform",
			Params: map[string]any{"min": 0.0, "max": 100.0}},
		{Name: "region", Type: "categorical_u8", Distribution: "weighted_categorical",
			Params: map[string]any{"values": []any{"north", "south"}, "weights": []any{0.7, 0.3}}},
	}}
	data, _, err := synth.SynthBytes(spec, synth.Options{Seed: 7})
	return data, err
}
```

Hand the bytes to `Pulse.InspectBytes` / `Pulse.PredictBytes`, or write
them through an `afero.Fs`. A spec stored as JSON goes through
`synth.ParseSpec`, which refuses unknown keys.

## Where the detail lives

- The spec format, distributions and calibration:
  [`synth from-schema`](../cli/synth-from-schema.md),
  [`synth from-profile`](../cli/synth-from-profile.md),
  [Synth calibration & rationale](../cli/synth-calibration.md); each
  distribution is in the [Synth distributions](../guide/catalog/synth_distribution.md)
  catalog.
- Profiles: [`profile create`](../cli/profile-create.md).
- GoDoc: [`synth`](https://pkg.go.dev/github.com/frankbardon/pulse/synth),
  [`Pulse.Synth`](https://pkg.go.dev/github.com/frankbardon/pulse#Pulse.Synth).
