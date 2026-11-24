# Execution modes — parallel total() (two-phase)

Per-topic slice of the execution-modes reference. Index (Mode index + section map): `.claude/reference/execution-modes.md` — load it first, then only the slice(s) your engine work touches. The reducers themselves are in `execution-modes/shards-and-merge.md`.

## Two-phase `total()`

**Two-phase `total()`.** A total-bearing `ATTR_FORMULA` is not row-local, yet both parallel reducers (per-shard and parallel decode) run it when `mergegate.ParallelTotals` admits it: every `total()` formula of a request with no `Crosstab` (and no nil attribute slot), grouped too (the cohort-wide total publishes before any grouper reads a row). The request's other attributes are judged as usual: a non-row-local one keeps the run serial.

- **Layers.** The serial two-pass plan (`twoPassPlan`) puts a formula one layer deeper when its input reads, directly or through row-local slots, an earlier `total()` formula's label. An `ATTR_REG_PREDICT`, fit-referenced or extension slot without `FieldInputs` reads every earlier one.
- **Phase 1** (`internal/service/totals_prepass.go`) is one parallel pre-pass per layer, in order, over the same partitions (shards on the per-shard arm, block-aligned segments on the decode arm), under the layer's own narrow projection (`processing.TotalsPrepassRequest`: filters, weights, zones, and only what a layer's `total()` arguments read — never a field only a row expression or an aggregation reads). Each worker binds every earlier layer's published values (a missing one is `PROCESSING_INTERNAL`, never a 0) and folds its filter-passing rows. Worker states merge through `MergeableTotals.PartialTotals` / `AbsorbTotals` — binned `reprosum` sums and integer counts, order-free, so the merged totals equal the serial pre-pass bit for bit.
- **Phase 2** is the ordinary reduce: each partition binds its formulas through `PublishedTotals.Bind` before its first row. An unbound formula refuses; it never reads 0.
- Predict follows the shared gate (`ShardFanOut` / `DecodeFanOut`), and `PULSE_ADVISORY_EXTRA_PASS` stays one added scan per formula.
- Pinned by the bit-exactness family (`TestBitExact_Archive` / `TestBitExact_Heavy`: each parallel cell must fan out and equal the serial bits) and `TestTotalsPrepass_*`.

## ProcessChain stages

**A chain stage runs `total()` two-phase too.** `mergegate.ChainRefusal` is `MergeRefusal` (crosstab first, matrices stripped) with the same `ParallelTotals` admission, so a `total()` stage passes wherever a Process request would merge; a refused one keeps `PULSE_CHAIN_NOT_MERGEABLE` with the blocking attribute's reason. The chain validator calls the same rule, so runtime and predict-chain agree per stage (`TestChainGate_ValidatorMatchesRuntime`).

- **Stage 0** runs `Process`, so it takes the arms above: two-phase when it fans out, the serial two-pass plan otherwise.
- **A later stage** never fans out (one in-memory input). A `total()`-bearing stage with rows runs the same two phases over the previous stage's rows as ONE partition (`internal/service/chain_totals.go`), rewinding the row iterator per layer, with every formula bound before its first row. Rows stay lazy and the stage reports the `streaming` arm; the answer is the serial processor's whole response byte for byte (`TestChainStageTotals_MatchesSerialProcessor`). A stage with an empty input, or no `total()`, takes the ordinary stage path.
- Pinned by `TestBitExact_ChainTotals` (any ShardWorkers ≡ the single file's serial chain, stage by stage, bitwise) and heavy peak-memory tests asserting the chain is not worse than serial.
