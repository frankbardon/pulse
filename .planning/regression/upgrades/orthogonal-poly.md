# orthogonal-poly — FEAT_ORTHO_POLY (Chebyshev / Legendre)

## Motivation
Phase 6 shipped `FEAT_POLY{Degree:n}` emitting raw powers `x, x², …, xⁿ`.
For `n ≥ 4` the Gram matrix `XᵀX` becomes badly conditioned —
correlations between `x²` and `x⁴` exceed 0.99 — and the OLS solver
emits `PROCESSING_REGRESSION_RANK_DEFICIENT` or numerically-unstable
coefficients. Orthogonal polynomial bases (Chebyshev or Legendre)
produce mutually orthogonal columns with `XᵀX ≈ diagonal`, giving
stable fits at `Degree = 6, 8, 10` where raw powers fail. Currently
blocked: any user who genuinely wants a high-degree polynomial fit
(rare but legitimate in physical-sciences contexts) is stuck.

## Proposed operator / spec shape
```go
const FEAT_ORTHO_POLY FeatureType = "FEAT_ORTHO_POLY"

// FeatureSpec additions (already has Field, Degree from FEAT_POLY):
Basis string // "chebyshev" (default) | "legendre"
// Range fields are needed because both bases are defined on [-1, 1]:
RangeMin float64 // domain lower bound; default = computed min
RangeMax float64 // domain upper bound; default = computed max
```

The defaults imply a two-pass feature: pass 1 computes the field range,
pass 2 emits the rescaled basis. Document this clearly in the skill.

## Algorithm sketch
- **Chebyshev T_n(x):** recurrence `T_0 = 1, T_1 = x, T_{n+1} = 2x·T_n − T_{n-1}`
  after rescaling x to `[-1, 1]`.
- **Legendre P_n(x):** recurrence `(n+1)P_{n+1} = (2n+1)x·P_n − n·P_{n-1}`.

Both are O(n) per row and stable. Output column names:
`{field}_cheb_1, {field}_cheb_2, ...` (or `_legendre_*`).

## Streamability
Streaming once the range is known. With explicit `RangeMin` / `RangeMax`
specified, fully streaming. With computed range, requires a min/max
pre-pass — same shape as the existing `FEAT_POLY` range handling
where the standardization is centered, if any.

## Error codes
- `PROCESSING_FEATURE_ORTHO_POLY_DEGENERATE_RANGE` —
  `RangeMin == RangeMax` (constant field).
- `PROCESSING_FEATURE_ORTHO_POLY_INVALID_BASIS` — Basis not in known
  set.
- `PROCESSING_FEATURE_ORTHO_POLY_OUT_OF_RANGE` — observed value outside
  `[RangeMin, RangeMax]` when user-supplied (warn vs. error: warn by
  default, error under `--strict`).

## Update Demand impact
- "registered feature operator" row fires →
  `skills/feature-engineering.md` + `descriptor/capabilities_features.go`.
- `types/streamability.go` entry.
- New error codes → fixup metadata + manifest.
- May want a comparison section in the skill: when to use `FEAT_POLY`
  vs. `FEAT_ORTHO_POLY`.

## Dependency cost
None.

## Estimated phase count
1 — engine + skill section + one example showing `Degree:8` working
where `FEAT_POLY` fails.

## Open questions
- Default basis: Chebyshev or Legendre? Numerically near-identical;
  Chebyshev is marginally more popular in applied work. Recommend
  Chebyshev default with Legendre as alternative.
- Should we ever auto-promote `FEAT_POLY` to `FEAT_ORTHO_POLY` when
  `Degree > 4`? No — that's a silent behavior change. Document in
  the skill instead.
- Two-pass behavior with auto-range: when ingested via the streaming
  process orchestrator, falls back to buffered. Acceptable, but flag.
