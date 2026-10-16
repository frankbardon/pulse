```yaml
name: decimal-formulas
description: Exact decimal128 arithmetic inside formulas and expression filters — operator result scales, the numeric built-ins over a decimal (abs, round, max, mean and the rest), ternary and ?? lifting, the float() escape, and what is refused at compile. Use when an expression reads a money field.
type: guide
kind: design
applies_to: process, compose, predict
covers: [decimal128, expression, formula, float, PULSE_DECIMAL_DIVIDE_BY_ZERO, PULSE_DECIMAL_OVERFLOW, PULSE_FORMULA_COMPILE_ERROR]
```

# Decimal formulas

Part of the expression surface; entry skills [`expression-language`](expression-language.md) (the environment) and [`financial-cohorts`](financial-cohorts.md) (money fields). In the formula attribute and the expression filter a `decimal128` field binds exactly — its digits plus its field's scale — and stays exact through every operator and numeric built-in below. A number joins as written (`1.2` is exactly 1.2), so `0.1 + 0.2 == 0.3` holds and `price / cost > 1.2` is decided exactly.

## Operators

| Op | Result |
|---|---|
| `+` `-` | scale `max(s1, s2)` |
| `*` | scale `s1 + s2` (held to 38, banker's) |
| `/` | scale `max(s1, s2, 4)`, banker's |
| `< > <= >= == !=` | exact verdict (against a non-finite number, IEEE) |
| unary `-` | same scale |
| `%` | refused at compile — write `float(price) % 2` |

## Mixed branches and defaults

`price ?? 0`, `price ?? x` and `cond ? price : 0` lift the other side to a decimal, so `(cond ? price : 0) * 2` and `(price ?? x) * 3` stay exact on every row, null rows included. A decimal carried through a map or a mixed list (`{"a": price}.a`) keeps its exact operators.

## Built-ins over a decimal

| Built-in | Rule |
|---|---|
| `abs`, `max`, `min` | exact; `max` / `min` answer a decimal, ties keep the first |
| `sum`, `mean`, `median` | exact; `mean` and an even-count `median` divide at `max(scale, 4)`, banker's |
| `ceil`, `floor`, `round` | an integral decimal (scale 0); `round` is half-even like every decimal rescale — `round(2.5)` is 2 on a decimal, 3 on a float |
| `int` | truncates toward zero; past the int range `PULSE_DECIMAL_OVERFLOW` |
| `sort`, `sortBy`, `in` | ordered and matched by exact value |
| `string`, `toJSON` | the decimal at its scale (`3.60`) |
| `sqrt`, `log`, `exp`, `pow`, vector functions | read the decimal's nearest f64 and answer f64 — the result is irrational, there is no exact form |

## Escape, results, failures

- `float(price)` is the explicit f64 escape: `float(price) * 1.1` is float math.
- A formula column is f64 (a decimal result lands as its nearest f64); a filter reads the exact verdict.
- Zero divisor → `PULSE_DECIMAL_DIVIDE_BY_ZERO`; a result past 38 digits → `PULSE_DECIMAL_OVERFLOW` — run errors, never null.
- A string or bool operand (`price + region`) and `%` fail at compile with `PULSE_FORMULA_COMPILE_ERROR`; predict refuses the same expression with the same message.
