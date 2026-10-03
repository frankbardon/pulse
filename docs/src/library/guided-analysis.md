# Guided-Analysis Vocabulary

**Audience:** Go embedders and agent-harness authors.

Pulse ships plain-language guidance metadata alongside its operators: what
kind of question each one answers, how to read the numbers it returns,
and a glossary of the statistical terms involved. The metadata is
**pulled on demand**. It never appears in a default `Response`,
`PredictResult` or manifest, apart from short intent IDs.

> **Status (v1.0.0 alphas).** The vocabulary and the data model are in
> place. Only a few operators have written guidance so far:
> `AGG_AVERAGE`, `TEST_ANOVA_F` and `TEST_PEARSON_R`. Every other
> operator carries no `intents` yet. Later units add guidance for the rest.

## Intents: what kind of question

An **intent** is a kind of question a non-statistician would recognise,
such as "compare groups", "relationship between measures" or "change
over time". The taxonomy is closed and is the same on every instance. A
feature profile never prunes it.

```go
for _, in := range pulse.Intents() {
    fmt.Println(in.ID, in.Label, in.Analytic)
    // in.Sounds: example phrasings; in.Shapes: the field roles it needs
}
```

Twelve intents are analytic, meaning operators answer them. Three
(`prepare`, `simulate`, `lookup`) are not: they point at tooling.
`Intent.Shapes` describes the data each intent needs as named roles,
for example one `outcome` field plus one categorical `group` field for
"compare groups".

The manifest carries the intents compactly:

- top-level `intents` is the sorted list of IDs only
- each operator, test, regression, distribution and overlay-kind entry
  carries `intents`, the IDs its guidance declares. The key is omitted
  when an entry declares none.

To find candidate operators for a question, pick its intent ID and
filter manifest entries whose `intents` contain it.

## Glossary

```go
for _, t := range pulse.Glossary() {
    fmt.Println(t.ID, "—", t.Short)
}
```

Each `descriptor.Term` has a one-sentence `Short`, a `WhyCare`, and
`SeeAlso` links. Both functions return fresh deep copies in declaration
order, so changing the result cannot affect Pulse.

## Effect sizes

Statistical tests report standardised effect sizes under
`details.effect_size` (for example `cramers_v` on `TEST_CHISQ`,
`omega_squared` on `TEST_ANOVA_F`, `rank_biserial` on
`TEST_MANN_WHITNEY_U`). A key is **omitted** when it is undefined for
the data, for instance when every value ties. Each test's skill
(`op-test-*`) lists its keys and formulas.

## Over MCP and the CLI

The glossary and the taxonomy are also served as two skills with no
file behind them, `glossary` and `intents`. They work everywhere skills
do:

- `pulse skills show glossary` / `pulse skills show intents`
- `pulse_skills_get` with `name: "glossary"` / `"intents"`
- the resources `pulse-skill://glossary` and `pulse-skill://intents`
- the manifest's `skills` list

## Guidance on your own operators

Extension registrations accept an optional `Purpose`, and test
registrations an optional `Interpretation`. `pulse.New` validates them
and refuses an invalid one with `PULSE_EXTENSION_PURPOSE_INVALID`. See
[Extension Points](../internals/extension-points.md#purpose-and-interpretation-guidance-metadata).
