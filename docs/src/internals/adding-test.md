# Adding a Statistical Test

**Audience:** internals contributors adding a new `TEST_*` operator —
tier-1 (row-stream) or tier-2 (post-test on the materialised result
set).

The recipe mirrors the aggregator and feature recipes; the
test-specific moving parts are streamability, the test catalog, and
the registered-test capability table.

> **From CLAUDE.md, "Update Demand" rows for statistical tests and
> tier-2 post-test variants.**

## 1. Decide tier

- **Tier 1.** Runs against the raw row stream, alongside aggregators.
  Online-moments tests (`TEST_T`, `TEST_WELCH`, `TEST_CHISQ`,
  `TEST_ANOVA_F`) stay in the streaming Process path. Sort-required
  tests (`TEST_KS`) force the buffered path.
- **Tier 2.** Runs after the result set is materialised, in
  `req.PostTests`. Always buffered.

## 2. Declare the type constant

Add to `types/types.go`:

```go
const (
    // ... existing constants ...
    TEST_GINI_TREND TestType = "TEST_GINI_TREND"
)
```

Add it to `types.AllTestTypes()`.

## 3. Implement and register

Tests live in `processing/test_*.go`. Existing examples to mirror:

- `processing/test_t.go` — online tier-1 test.
- `processing/test_anova.go` — tier-1 ANOVA with grouper support.
- `processing/test_post.go` and `processing/test_post_more.go` —
  tier-2 post-tests.
- `processing/test_studentized.go` — numerical integration utilities
  (used by `TEST_TUKEY_HSD`).

Register the test in `processing/test.go` (the registry construction
calls). For tier-2 variants, declare both the base type and the
variant identifier the post-test surface uses.

## 4. Streamability

Add a case in `types/streamability.go` for the new `TestType`:

```go
func (t TestType) Streamable() bool {
    switch t {
    // ...
    case TEST_GINI_TREND:
        return false // sort-based
    }
}
```

Add the matching row in `types/streamability_test.go` so
`TestStreamability_TestsKnown` passes.

## 5. Capability declaration

Add a row to `descriptor/capabilities_tests.go`:

- For a tier-1 test, declare it in the tier-1 catalog (`testCapabilities`).
- For a tier-2 post-test, declare it in `postTestCapabilities`.

`TestManifestTestsComplete` and `TestManifestPostTestsComplete`
enforce that the manifest enumerates every registered test.

## 6. Skill update

Add an entry to `skills/statistical-testing.md` under "Operator
catalog". Describe the test's family, inputs, outputs (statistic, p,
df, effect size, …), and any preconditions (`PULSE_TEST_*` error
codes it can raise). For tier-2 variants, also document the variant
field shape since the post-test API exposes it.

## 7. Tests

Use the same TDD pattern as for aggregators. The processing package
has rich existing test files to model new cases against:
`processor_test_pipeline_test.go`, `test_parametric_test.go`,
`test_nonparametric_test.go`, `test_post_more_test.go`. Add hermetic
fixtures that exercise the streaming and buffered paths.

## 8. Error codes

If your test introduces a new failure mode, add a code to
`errors/codes.go` (mirror the existing `PULSE_TEST_*` family),
register its description row in `descriptor/capabilities_errors.go`,
and document recovery in `errors/fixup_metadata.go` (`codeMetadata`
Message + Fixups, surfaced per-code via `pulse_errors_lookup` /
`pulse errors lookup CODE`). See the
[Adding an Aggregator](adding-aggregator.md) recipe for the same
pattern at the aggregator layer.

## 9. CLAUDE.md

Update CLAUDE.md's "Current registered components → statistical
tests" line with the new operator. If the test introduces a new
preconditions class (e.g. paired sample, repeated measures), also
add a sentence describing it in the parent paragraph.

## 10. Run the gates

```bash
go test ./processing/ -run TestType_Streamable
go test ./types/    -run TestStreamability_TestsKnown
go test ./descriptor/ -run TestManifest
go test ./skills/    -run TestSkillsCoverAll
go test ./...
```

See [The Update Demand](update-demand.md) for the full row that
governs statistical-test changes.
