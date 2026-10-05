# p.adjust reference fixtures

`gen_padjust_reference.R` writes `../../reference_values_test.go`, the
external reference values `TestMultiplicityReferenceValues` pins the
correction core against. CI never runs R; the generated Go file is the
oracle. Never hand-edit it — change the script and regenerate.

## Regenerate

```sh
cd internal/processing/multiplicity/testdata/padjust_reference
Rscript gen_padjust_reference.R            # base R only, no packages
gofmt -w ../../reference_values_test.go
go test ../.. -run TestMultiplicityReferenceValues
```

Pinned toolchain: R 4.6.1 (the script refuses any other version), base
`stats::p.adjust` only. The large-m vectors come from `set.seed(20261005)`
and `runif`, so the same R version reproduces them exactly.

## Methods

| Pulse method | R `p.adjust` method |
|---|---|
| `bonferroni` | `"bonferroni"` |
| `holm` | `"holm"` |
| `bh` | `"BH"` |
| `by` | `"BY"` |
| `none` | `"none"` |

## Cases

Empty input, m = 1 (alone and among NaN), all-NaN, m = 2, distinct
sorted / unsorted, ties, all-equal, NaN interspersed, exact 0 and 1,
capped results, tiny p (down to 1e-300), a non-monotone step that
exercises the running max / min, and two large vectors (m = 500
continuous; m = 300 rounded to two decimals, so heavily tied).

NaN in Go is NA in R: `p.adjust` drops it from n and returns it in
place, which is the contract the core mirrors. Bonferroni, Holm and BH
agree with R bit-for-bit; BY's harmonic sum is accumulated in long
double by R, so the test compares at 1e-13 relative.
