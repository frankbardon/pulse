# Matrix reference fixtures

`gen_matrix_reference.py` writes `../../matrix_reference_values_test.go`,
the external reference values `TestMatrixCovariance_MatchesReference`
pins (contract: `.claude/reference/matrix-and-vectors.md`, Matrix slot).
CI never runs Python; the generated Go file is the oracle. Never
hand-edit it.

## Regenerate

```sh
cd internal/service/testdata/matrix_reference
uv run --no-project gen_matrix_reference.py   # versions pinned in the PEP 723 header
gofmt -w ../../matrix_reference_values_test.go
go test ../.. -run TestMatrixCovariance_MatchesReference
```

Pinned toolchain: Python 3.12, numpy 2.3.3, statsmodels 0.14.5. The
script's docstring maps each Pulse definition onto the library call
configured to match it (`numpy.cov` unweighted, `DescrStatsW(weights,
ddof).cov` weighted, `numpy.linalg.det` for the determinant).
