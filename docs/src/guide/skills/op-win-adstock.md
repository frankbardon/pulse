```yaml
name: op-win-adstock
description: Media adstock; a finite lag kernel (geometric, Weibull CDF or Weibull PDF) convolved over the ordered partition.
kind: operator
category: WIN
operator: WIN_ADSTOCK
type: reference
applies_to: process, compose, predict
examples_tags: [time-series, feature-engineering, window-operator, buffered-pipeline]
```

Window operators emit row-level values; they do not produce `Response.Components`.

## Use when

Adds a carry-over series: each row sums the current and earlier values, weighted by a decaying curve, so spend keeps acting later.

Questions it answers:

- How much of last month's advertising is still working this week, as a column a model can use?
- What does each channel's spend look like once its lingering effect is spread over later weeks?

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `kernel` | enum | — (required) | `geometric` \| `weibull_cdf` \| `weibull_pdf` |
| `max_lag` | int | — (required) | last lag, `[1, 10000]`; `max_lag + 1` weights |
| `decay` | float | — | geometric only, `[0, 1)`: `w_l = decay^l` |
| `shape` / `scale` | float | — | Weibull only, both `> 0` |
| `normalize` | bool | `true` | weights divided by their sum |

Weibull kernels: `weibull_cdf` `w_l = exp(−(l/scale)^shape)` (survival, `w_0 = 1`); `weibull_pdf` `w_l = f(l + 1)` (density one lag on). `partition_by`, `order_by` (≥1), `field` (numeric) required; `frame` refused.

## Inputs

| Param | Accepted field types |
|---|---|
| `Field` | numeric: `u4`/`u8`/`u16`/`u32`/`u64`, `f32`/`f64`, `date`; others → `PULSE_FIELD_TYPE_UNSUPPORTED` |

## Output

`y_i = Σ_l w_l · x_{i−l}`, one `float64` per row in `Label`.

## Reading the output

- `value`: The field's carried-over level: the current value plus earlier values, each weighted by the kernel at its distance back, up to max_lag rows. With the default normalisation the weights sum to 1, so the value is a weighted average of recent values on the field's own scale.
  - Caveat: The first rows of each partition have fewer earlier rows to draw on, so they read lower than a long series would at the same spend; read them with care.

## Gotchas

- Lags before the partition start or on a null row add 0 (weights not renormalised); a null row emits `null`.
- Starts from zero, unlike `WIN_EWMA`; normalised geometric ≈ EWMA (`alpha = 1 − decay`) once `decay^i` is negligible.
- Missing/unknown `kernel`, bad `max_lag`, `decay`, `shape`, `scale`, a param the kernel does not read → `PULSE_WINDOW_INVALID` (predict and runtime).
- Not weightable; forces buffered execution.

## See

- `pulse_examples_search tags=[time-series, feature-engineering]`
- Skills: [`op-win-ewma`](op-win-ewma.md), [`window-design`](window-design.md), [`expression-language`](expression-language.md)
