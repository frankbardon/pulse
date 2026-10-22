# Flows between states

## The question

Questions of this kind sound like:

- Where do customers move between states?
- Who switched from A to B?

## Pick your analysis

This question fits data shaped like:

- `from`: one categorical or bool field; `to`: one categorical or bool field

The operators that answer it, by kind. Start with a basic one; each name links to its catalog entry.

### Attributes

| Use | In plain words | Level |
|---|---|---|
| [`ATTR_PREVIOUS_STATE`](../catalog/attribute.md#op-attr_previous_state) | Adds to each row of a long panel the state the same subject held at its previous date, so moves can be counted from-state by to-state. | intermediate |

### Overlays

| Use | In plain words | Level |
|---|---|---|
| [`OVERLAY_RETENTION`](../catalog/overlay.md#op-overlay_retention) | Retention rates of a from-state by to-state crosstab: for each starting state, the share that stayed, switched or churned. | basic |
| [`OVERLAY_SYMMETRY`](../catalog/overlay.md#op-overlay_symmetry) | Test of symmetry on a before-by-after crosstab of the same units: are moves between two states as common one way as back? | intermediate |
| [`OVERLAY_MARKOV`](../catalog/overlay.md#op-overlay_markov) | Markov chain of a from-state by to-state crosstab: transition probabilities, n-step projections, steady state and time to absorption. | advanced |

## A worked example

No worked example is published for this question yet. `pulse recommend --intent flows` drafts requests for it.

## How to read the result

The figures these operators return read as their names say; for the ones that need more, see [Reading your results](../reading.md).

## Pitfalls

Each operator's catalog entry lists its assumptions and when to reach for something else; read them before trusting a figure.

## Next steps

**Skills:** [`intents`](../skills/intents.md), [`attribute-composition`](../skills/attribute-composition.md), [`overlay-system`](../skills/overlay-system.md), [`pairwise-n-sources`](../skills/pairwise-n-sources.md)

**From the command line:**

- `pulse recommend --intent flows --cohort PATH` drafts requests for this question bound to your cohort's fields.
- `pulse api predict` checks a request against your cohort without running it.
- `pulse api process` runs it.
- `pulse explain --response FILE` reads a result back in plain words.
