Pulse checks its inferential figures (test statistics, p-values, intervals, regression coefficients and standard errors, matrix results and significance overlays) against established external implementations wherever one exists. This page lists, operator by operator, what each is checked against, how tightly, and where the check lives, so you can see why to trust a number and where the evidence is thinner.

**How the checks work.**

- **R goldens.** `make reference` runs pinned R scripts (`scripts/reference/`) on R 4.6.1, with versions pinned for psych, car, ppcor, corpcor, GPArotation, sandwich, lmtest, biotools, nortest, survey, glmnet, lavaan and others. Each number is written at full precision (`%.17g`) together with the exact input data. Every golden file ends in a SHA-256 footer that the tests check, so a value cannot be edited by hand without the test failing. CI never runs R: the goldens are committed, and the Go tests compare against them on every `go test`, typically at a relative tolerance of 1e-10.
- **Weighted reference.** For weighted analysis, a separate harness (`internal/service/testdata/weight_reference/`) checks the weight-aware tests, regressions and overlays. For frequency weights it runs the stock R function on rows repeated by their weight (`rep()`). For probability weights it uses a closed form on Kish-rescaled weights with scipy tails, cross-checked with statsmodels. The tolerance is 1e-12 relative.
- **Multivariate and SEM.** Matrix operators are checked against numpy, statsmodels and the R packages named in each row. Structural equation models are checked against lavaan and cross-checked with semopy.
- **Undefined figures.** Edge cases where R returns `NaN` or `Inf` are pinned too: Pulse reports those as `null`, never as a made-up number.

**Reading a row.**

- *Validated against* names the reference and its version.
- *Tightest tolerance* is the strictest comparison the tests apply.
- *Gap* says plainly where the evidence is weaker: a loose or hand-computed check, no weighted check, or no external implementation at all.

The registry behind this page is gated: every test file it cites must exist, and every built-in test, regression, matrix operator and inferential overlay must have a row. An operator is never silently missing from this page.

To regenerate the goldens, see *Regenerating goldens* in the contributor guide.
