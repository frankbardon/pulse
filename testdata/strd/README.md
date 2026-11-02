# NIST Statistical Reference Datasets (StRD)

Unmodified copies of the NIST/ITL StRD files, downloaded 2026-10-31 from
<https://www.itl.nist.gov/div898/strd/>:

- `lls_*.dat`: linear least squares regression (11 datasets).
- `anova_*.dat`: one-way analysis of variance (11 datasets).
- `univ_*.dat`: univariate summary statistics (9 datasets).

Each file carries NIST's certified values and the data. The files are US
Government works and are reproduced verbatim, CRLF line endings included
(`.gitattributes` marks them `-text`). `SHA256SUMS` pins every byte;
`TestStRD_FixturesIntact` fails on any change. Never edit a `.dat` file.

`strd_reference_test.go` (repository root) runs each dataset through the
public API and computes the log relative error (LRE, digits of agreement
with the certified value). `lre.json` records the digits achieved and is
rewritten only by `go test -run TestStRD -update`; it carries a
`// golden-hash:` footer like every other golden.
