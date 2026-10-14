# Predict / Inspect — history (NOT loaded by default)

Superseded wording relocated out of the live file at E9-S4; do not load it for contract work — the index `.claude/reference/predict-inspect.md` and its slices are live.

## The CLAUDE.md Inspect bullet, pre-compression (relocated verbatim at weighting-descriptive E1-S1)

- **Inspect header-only:** reads only `encoding.ReadHeader` + `encoding.ReadSchema`; dictionaries truncated to `DefaultDictionaryLimit` (100) unless `FullDict: true`. `InspectResult.RecordCount` is derived from the file LENGTH, never by reading a record. **`Pulse.Inspect` drops `env.Warnings` on the floor** — `Pulse.InspectEnvelope` is the only way to reach the `FullDict` knob or the truncated-tail warning, and both the CLI leaf and `pulse_inspect` therefore read the envelope, never the result-only wrapper.
