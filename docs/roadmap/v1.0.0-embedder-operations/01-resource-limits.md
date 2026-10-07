# 01 — Resource limits

## Principles

1. **High defaults.** The default configuration should essentially never block a legitimate request on realistic hardware. Limits exist so an embedder *can* tighten them, and so a pathological request, such as a group-by on a unique ID, fails clearly instead of exhausting memory.
2. **Predict first.** Every limit that can be judged from the schema and request alone is checked at predict, before a record is read. Runtime checks catch what can only be known while running.
3. **Every refusal teaches tuning.** Each limit error names the limit, its configured value, the observed or estimated value, and the exact option to change, via the fixup template.
4. **Raising defaults is compatible; lowering them is not.** This is recorded in `STABILITY.md`.

## The limits

| Limit (`Options.Limits.*`) | Guards against | Checked at | Proposed default |
|---|---|---|---|
| `RequestTimeout` | a request that runs far longer than intended (applied as an inner deadline, in addition to the caller's context) | runtime | **0 = none**. The caller's context governs; set it for MCP / shared hosts |
| `MaxGroups` | high-cardinality group-bys (grouping on an ID) that explode memory | predict (from dictionary sizes where known) + runtime | **10,000,000** distinct group keys |
| `MaxCrosstabCells` | huge row × column grids | predict (dictionary sizes) + runtime | **10,000,000** cells |
| `MaxEstimatedMemory` | requests whose buffered state cannot fit | predict (estimate) | **0 = derive**: 80% of `GOMEMLIMIT` when the host set one, otherwise unlimited |
| `MaxMatrixDim` | quadratic matrix outputs (vector-matrix theme) | predict | **2,048** columns. This raises the 256 floated in vector-matrix 02, consistent with "high defaults" |
| `MaxComposeSlots` / `MaxChainStages` | fan-out requests | validate | **1,000** |
| `MaxJoinBuildRows` | hash-join build side held in memory | runtime | **100,000,000** |
| `MaxOutputRows` | — | — | **not added.** Output volume is the developer's to manage (decided); see response shaping for *what* is returned |

All limits accept `0` = unlimited (except where "derive" is noted), and negative values are rejected at `pulse.New`, matching the existing `ShardWorkers` / `DecodeWorkers` rule.

## Behaviour when a limit trips

- **Predict:** `PredictResult.LimitFindings[]` reports `{limit, configured, estimated}`. Process refuses before scanning, with the same coded error.
- **Runtime:** stop at the first breach, release buffers, return `PULSE_LIMIT_EXCEEDED` with `details {limit, configured, observed, option}` and the fixup "raise `Options.Limits.MaxGroups` (currently 10,000,000) or reduce the grouping cardinality". There are no partial results.
- **Streaming:** a breach ends the stream with the coded error as its terminal event.
- **Compose:** the slot fails; `FailFast` governs whether its siblings continue (existing semantics).

## Configuration surfaces

- `pulse.Options.Limits` (Go).
- An optional `limits` section in a feature-profile file. Omitted keys mean the defaults above.
- The manifest's `limits` block reports the instance's effective values. Agents can then shape requests up front, e.g. avoid grouping on a field whose dictionary exceeds `MaxGroups`. This reveals no hidden feature, so it is profile-safe.
- `pulse mcp --limit max_groups=1000000`, for MCP servers.

## Gates

- **`TestLimitsDefaultsNeverTripGoldens`:** every example and golden request runs under the defaults without a limit finding.
- **`TestLimitsPredictRuntimeParity`:** a limit predict says will trip does trip at runtime with the same code, and vice versa, wherever predict can know.
- **`TestLimitsReleaseMemory`:** after a runtime trip, the buffered state is released (heap check).

## Deliverables

- [ ] `Options.Limits` with the defaults above; validation at `pulse.New`
- [ ] Predict-time checks + `PredictResult.LimitFindings`
- [ ] Runtime checks in grouper, crosstab, join build, matrix and compose paths
- [ ] `PULSE_LIMIT_EXCEEDED` with tuning fixups
- [ ] Profile-file `limits` section; manifest `limits` block; `pulse mcp --limit`
- [ ] Defaults-never-trip, parity and memory-release gates
- [ ] Embedder docs page "Tuning limits"

## As landed (U19)

The deviations from the proposal above, all deliberate:

- **Encoding.** `0` means "use the default" and `-1` (`pulse.Unlimited`) means no limit, on every layer (Go, profile, `--limit`); the proposal's `0` = unlimited was dropped because a zero value could not then be both "unset" and "unlimited". Other negatives fail `pulse.New` with `PULSE_LIMIT_INVALID`.
- **`MaxEstimatedMemory`.** Default is unlimited, not "80% of `GOMEMLIMIT`". There is no `GOMEMLIMIT` coupling; an embedder opts in with a byte count. The estimate is an upper bound (about 1.3x to 4.9x the measured peak heap) and is checked at predict and again as a pre-flight before any record decodes.
- **Grades.** Each `LimitFinding` is `certain` or `possible`. Only `certain` findings refuse (`valid` false); `MaxGroups` and `MaxCrosstabCells` are `possible` because they come from dictionary-size upper bounds.
- **`limits_digest`.** The manifest's `limits` block ships with `limits_digest` (`lim1:` + SHA-256), outside `feature_set_digest` (limits are behaviour, not features).
- **Facet.** Covered: the rich facet has a memory pre-flight and `RequestTimeout`; the simple `pulse.Facet(path, field)` has no pre-flight.
- **`RequestTimeout`.** Covers the whole Compose / ComposeParallel / ProcessChain call and the `ProcessStreamResult` drain; only the inner deadline is `PULSE_LIMIT_EXCEEDED`.
- **Streaming.** `ProcessStream` is buffered, so a breach errors before the stream exists; a mid-stream timeout ends `ProcessStreamResult` with the coded error.
- **Compose.** `FailFast=false` returns `(nil, SERVICE_INTERNAL)` rather than partial slots (the doc previously claimed otherwise).
