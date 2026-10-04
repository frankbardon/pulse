# Weighted reference fixtures

`gen_weight_reference.py` writes `../../weight_reference_values_test.go`,
the external reference values `TestWeightReferenceValues` and
`TestWeightReferenceKindsAgree` pin (contract:
`.claude/reference/weighting.md`, "Reference fixtures"). CI never runs
Python; the generated Go file is the oracle. Never hand-edit it.

## Regenerate

```sh
cd internal/service/testdata/weight_reference
uv run --no-project gen_weight_reference.py   # versions pinned in the PEP 723 header
gofmt -w ../../weight_reference_values_test.go
go test ../.. -run TestWeightReference
```

Pinned toolchain: Python 3.12, numpy 2.3.3, scipy 1.16.2,
statsmodels 0.14.5. The script's docstring maps each Pulse definition onto
the library call configured to match it, and refuses a fixture where a
normalized cumulative weight lands on an integer rank (a "knife edge",
where the probability quantile turns on the last ulp of a float sum).

## Probability-weight quantiles vs Hmisc (open decision)

No library implements Pulse's `kind: probability` median / percentile
(weights rescaled to sum to n, then x₍ₖ₎ = the smallest value whose
normalized cumulative weight EXCEEDS the 0-based rank k). Its reference
is an exact-rational transcription of that documented rule, checked
against numpy type 7 on the expansion where the normalized weights are
integers.

Hmisc `wtd.quantile(normwt = TRUE)` uses the same normalization but takes
the smallest value whose cumulative weight is >= the 1-based rank. With
fractional cumulative weights the two differ. One-off comparison on this
fixture (Hmisc 5.3.0, R 4.6.1; rows with a null value or an invalid
weight dropped first):

| weights | figure | Pulse | Hmisc |
|---|---|---|---|
| `w` probability | median | 3.5 | 4.25 |
| `w` probability | p37.5 | 3.5 | 3.5 |
| `w` probability | p90 | 6.55 | 7.275 |
| `p` probability | median | 3.5 | 3.5 |
| `p` probability | p37.5 | 2.65625 | 3.5 |
| `p` probability | p90 | 5.0 | 6.425 |
| `f` frequency (no normalization) | p90 | 6.9 | 6.9 |

```r
library(Hmisc)
x <- c(3.5, 1.25, 7, 3.5, 2, 9.75, 1.25, 3.5, 5, 4, 6.5)
w <- c(0.8, 1.7, 0.35, 2.2, 0, 1.15, 0.6, 1.3, 2.7, 0.45, 0.95)
wtd.quantile(x, w, probs = c(.5, .375, .9), normwt = TRUE)
```

Hmisc is not a CI dependency and these figures are not pinned; they
record why the probability reference is a transcription of Pulse's
rule, not a library value.
