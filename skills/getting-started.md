---
name: getting-started
description: Pulse vocabulary, file format, mental model
type: guide
applies_to: process, compose, sample, facet, inspect, predict, manifest
---

# Getting Started with Pulse

## Overview

Pulse is a high-performance tabular data processing engine built around a compact binary format called `.pulse`. It is designed for cohort-level analytics where schema fidelity, null handling, and categorical encoding matter.

## Core Vocabulary

- **Cohort**: A dataset stored in `.pulse` binary format. Each cohort has a schema header followed by fixed-width records.
- **Schema**: The field definitions (name, type, description, nullability) that describe the structure of a cohort.
- **Field**: A single column within a cohort schema, identified by name and typed with one of the 15 field types.
- **Record**: A single row of data within a cohort, encoded as a fixed-width binary block.
- **Aggregation**: A computation applied across records to produce summary statistics (e.g., COUNT, SUM, AVERAGE).
- **Attribute**: A derived value computed per-record from existing fields (e.g., z-score, formula).
- **Filterer**: A predicate that includes or excludes records before processing.
- **Grouper**: A partitioning strategy that segments records into groups before aggregation.
- **Request**: A JSON object describing the processing pipeline: cohort, filters, aggregations, attributes, groups, output config.
- **ComposedRequest**: A batch of multiple Request objects sharing a cohort.
- **Manifest**: The self-describing root object listing all commands, components, field types, and skills.

## The .pulse File Format

The `.pulse` format is a binary columnar format optimized for:

- Fixed-width records for O(1) random access
- Bit-packing for boolean and small nullable fields
- Inline categorical dictionaries for string-valued columns
- Schema-first design: the header encodes all field metadata

## CLI Command Tree

Pulse provides the following CLI leaf commands:

### `process`

Execute a processing request against a cohort. Accepts a JSON request and returns aggregation results.

```
pulse api process --request FILE [--json]
```

### `compose`

Execute multiple processing requests in batch using ComposedRequest. Runs all requests against a shared cohort and returns combined results.

```
pulse api compose --request FILE [--json]
```

### `sample`

Return sample rows from a cohort for data inspection.

```
pulse api sample --input FILE --count N
```

### `facet`

Return distinct values for a field. Useful for exploring categorical distributions and validating data.

```
pulse api facet --input FILE --field NAME
```

### `inspect`

Inspect a .pulse file header and schema. Shows field names, types, record counts, and dictionary contents.

```
pulse cohort inspect PATH [--json] [--full-dict]
```

### `predict`

Validate a request without executing. Reports warnings, errors, and compatibility issues. Use with `--strict` for fail-on-warning behavior.

```
pulse api predict --request FILE --json [--strict]
```

### `manifest`

Output the root manifest describing all commands, components, field types, and skills.

```
pulse --json
```

## Processing Pipeline

The processing pipeline follows this order:

1. **Load** the cohort from a `.pulse` file
2. **Filter** records using the specified filterers
3. **Group** filtered records by grouper configuration
4. **Aggregate** within each group
5. **Compute** derived attributes
6. **Output** results in the requested format

## JSON Envelope

Every CLI leaf supports `--json` output, which wraps results in a structured envelope with metadata, data, and optional warnings.

## Next Steps

- Read **cohort-schema-design** to learn about field types and schema definition
- Read **aggregation-guide** to understand available aggregators
- Read **error-code-reference** for troubleshooting
- Read **import-best-practices** for creating cohorts from external data
