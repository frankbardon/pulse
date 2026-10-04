# Weighted reference fixtures

`gen_weight_reference.py` writes `../../weight_reference_values_test.go`,
the external reference values `TestWeightReferenceValues` and
`TestWeightReferenceKindsAgree` pin (contract:
`.claude/reference/weighting.md`, "Reference fixtures"). CI never runs
Python or R; the generated Go file is the oracle. Never hand-edit it.

## Regenerate

```sh
cd internal/service/testdata/weight_reference
# once: R 4.6.1 + Hmisc 5.3.0 in a private library (see hmisc_quantile.R)
mkdir -p rlib
R_LIBS=rlib Rscript -e 'install.packages("remotes", repos = "https://cloud.r-project.org")' \
  -e 'remotes::install_version("Hmisc", "5.3.0", repos = "https://cloud.r-project.org")'
R_LIBS=rlib uv run --no-project gen_weight_reference.py   # Python versions pinned in the PEP 723 header
gofmt -w ../../weight_reference_values_test.go
go test ../.. -run TestWeightReference
```

(Keep `rlib/` out of the commit.) Pinned toolchain: Python 3.12, numpy
2.3.3, scipy 1.16.2, statsmodels 0.14.5; R 4.6.1, Hmisc 5.3.0 (the R
script refuses any other Hmisc version). The script's docstring maps
each Pulse definition onto the library call configured to match it.

## Probability-weight quantiles: Hmisc

Pulse's `kind: probability` median / percentile IS Hmisc
`wtd.quantile(x, w, probs, type = "quantile", normwt = TRUE)`: weights
rescaled to sum to n, x₍ₖ₎ = the smallest value whose cumulative weight
is >= the 1-based rank k + 1, linear interpolation. The reference is
Hmisc itself, via `hmisc_quantile.R`; the generator asserts each Hmisc
figure equals an exact-rational transcription of the rule to 1e-12
relative (Hmisc interpolates as `(1 - f)·lo + f·hi`, Pulse as
`lo + f·(hi - lo)`, so they can differ in the last ulps).

Pulse's earlier rule (the smallest value whose normalized cumulative
weight EXCEEDS the 0-based rank) agreed with Hmisc only on integer
cumulative weights; the effort owner adopted Hmisc's. The figures that
moved:

| weights | figure | earlier Pulse | Hmisc 5.3.0 (now Pulse) |
|---|---|---|---|
| `w` probability | median | 3.5 | 4.25 |
| `w` probability | p37.5 | 3.5 | 3.5 |
| `w` probability | p90 | 6.55 | 7.275 |
| `p` probability | median | 3.5 | 3.5 |
| `p` probability | p37.5 | 2.65625 | 3.5 |
| `p` probability | p90 | 5.0 | 6.425 |
| `f` frequency (no normalization) | p90 | 6.9 | 6.9 |

## Knife-edge guard

The generator refuses a fixture where a normalized cumulative weight
lands on an integer rank. Pulse snaps any cumulative weight within 1e-9
relative of an integer to it before comparing with a rank, so its answer
there is the exact-arithmetic one on every platform; Hmisc does not
snap, so its figure on such a fixture turns on the last ulp of a float
sum and is not a usable reference.
