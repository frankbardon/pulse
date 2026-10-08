# Guided-Analysis Vocabulary

**Audience:** Go embedders and agent-harness authors.

Pulse ships plain-language guidance metadata alongside its operators: what
kind of question each one answers, how to read the numbers it returns,
and a glossary of the statistical terms involved. The metadata is
**pulled on demand**. It never appears in a default `Response`,
`PredictResult` or manifest, apart from short intent IDs.

> **Status (v1.0.0 alphas).** Every operator carries a `Purpose`, and
> every statistical test, overlay and regression an `Interpretation`.
> Pulse renders them for you: the book's [Analysis Guide](../guide/index.md)
> (operator catalog, "Reading your results", glossary), the `## Use when`
> and `## Reading the output` sections of every operator skill, and
> `p.ExportReference` / `pulse docs export` for your own instance. The
> prose is still pulled, never pushed: it appears in none of the default
> `Response`, `PredictResult` or manifest. Recommend, Explain and the
> MCP guidance tools come in later units.

## Intents: what kind of question

An **intent** is a kind of question a non-statistician would recognise,
such as "compare groups", "relationship between measures" or "change
over time". The taxonomy is closed, and `pulse.Intents()` returns all of
it on every instance.

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

- top-level `intents` is the sorted list of IDs only. On an instance
  with a feature profile it leaves out an intent that only hidden
  operators serve; an intent no operator serves always stays
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

## Reading bands

Where an effect size has a published benchmark, its interpretation
labels value ranges as `very small`, `small`, `medium` or `large`, and
names the convention the labels come from. Every built-in band set comes
from one sourced registry of conventions, for example Cohen (1988) for
`cohens_d` (0.2 / 0.5 / 0.8) and for `eta_squared` / `omega_squared`
(0.01 / 0.06 / 0.14). Odds-ratio and R² bands are Cohen's benchmarks
converted to that scale, and the citation says so.

Bands are a labelled convention, not a verdict: what counts as a large
effect depends on the field. Effect sizes with no published convention,
such as `rank_biserial` or `cramers_v`, are deliberately left unbanded,
and their interpretation says why.

## Over MCP and the CLI

The glossary and the taxonomy are also served as two skills with no
file behind them, `glossary` and `intents`. They work everywhere skills
do:

- `pulse skills show glossary` / `pulse skills show intents`
- `pulse_skills_get` with `name: "glossary"` / `"intents"`, and
  `p.Skill("glossary")` / `p.Skill("intents")` in Go
- the resources `pulse-skill://glossary` and `pulse-skill://intents`
- the manifest's `skills` list

Both skills are listed on every instance. On an instance with a
feature profile, the bodies leave out what the instance cannot use: an
intent that only hidden operators serve, and a glossary term that only
hidden operators cite. "See also" links to a removed term are dropped
too. An intent no operator serves, and a term no operator cites, always
stay.

## Exporting the reference

`p.ExportReference(fs, dir, pulse.ExportReferenceOptions{})` writes
the instance's whole analysis reference as Markdown under `dir` on an
`afero.Fs` (a nil `fs` writes through the instance filesystem): the
operator catalog with each operator's purpose, the "Reading your
results" pages, the glossary, every skill as the instance serves it,
and an mdBook `SUMMARY.md` fragment. `OmitSkills: true` leaves the
skill pages out. The tree is deterministic, so two exports of the same
instance are byte-identical, and it is scoped like every other
surface: under a feature profile no page names a hidden operator, MCP
tool or request slot.

The export writes a `.pulse-docs-export` marker that lists the files
it wrote. An empty or missing directory, or one carrying the marker,
is written in place, and the files a previous export listed but this
one no longer renders are deleted; nothing else is touched. A
non-empty directory without the marker is refused with
`PULSE_DOCS_EXPORT_DIR_NOT_EMPTY` before anything is written. A
filesystem failure is `DATA_FILE`, with the path under
`details.path`.

From the command line, `pulse docs export --out DIR` does the same for
a default instance built over an in-memory filesystem, so it needs no
`PULSE_DATA_DIR`. `--feature-profile PATH` scopes the export to the
feature profile at that host OS path, `--no-skills` sets `OmitSkills`,
and `--json` wraps the result in the standard envelope, where a refusal
keeps its own code (`errors[0].code`).

## Guidance on your own operators

Extension registrations accept an optional `Purpose`, and test
registrations an optional `Interpretation`. `pulse.New` validates them
and refuses an invalid one with `PULSE_EXTENSION_PURPOSE_INVALID`. See
[Extension Points](../internals/extension-points.md#purpose-and-interpretation-guidance-metadata).
