---
name: type-packed-bool
kind: type
description: 1-bit boolean; bit-packed into one whole record byte of its own.
type: reference
applies_to: inspect, predict
---

## Bytes

Bit-packed (`IsBitPacked()` is `true`, `ByteSize()` is 0), yet each `packed_bool` takes one whole record byte (`Schema.FieldWidth` = 1); the value is `(b >> BitPosition) & 1`, and Pulse writers set `BitPosition` 0. Fields never share a byte, so field order does not change the stride.

## Range

`0` (false) or `1` (true); record rows render the number. Inference picks it for a source column whose sampled values are all `true`/`false`, `yes`/`no`, `t`/`f`, `y`/`n` or `1`/`0` (any case) — so a 0/1 integer column becomes `packed_bool`; a later non-boolean cell is a row error. Analytics-numeric (`IsNumericForAnalytics()`: mean = positive rate, sum = positive count); strict `IsNumeric()` is `false`.

## Null

Orthogonal: `Nullable: true` puts the field in the per-record null bitmap, independent of the value bit. `0` is false, never null; a mean runs over the non-null records.

## Dictionary

Absent.<!-- feature: GROUP_CATEGORY --> The default grouper is `GROUP_CATEGORY` (two buckets).<!-- /feature --><!-- feature: AGG_MODE_COUNT --> The default aggregator is `AGG_MODE_COUNT`.<!-- /feature -->

## Synth

`profile create` summarises a `packed_bool` as numeric, and a profile-driven synth rebuilds it as `bernoulli` with `p` = the observed mean, ahead of `--fit-shape`, so a boolean never becomes a mixture. A continuous marginal is not a valid reconstruction: one bit forces the writer to threshold, and a thresholded clamped normal gives the wrong prevalence. A modelled boolean draws through a step quantile, which makes it a probit: coefficients order rows, they are not probability changes.

## See

- Skill: `cohort-schema-design` (Field-type matrix, Bit-packed runs).
- Skills: `op-synth-bernoulli`, `synth-marginals` (Boolean marginals).
- Cross-link: `type-u4` (the other bit-packed type).
