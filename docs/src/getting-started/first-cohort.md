# Your First Cohort

**Audience:** new CLI users with `pulse` installed and `PULSE_DATA_DIR`
set ([Installation](installation.md)). Run these in the data directory.

## 1. Write a CSV

```bash
cat > sales.csv <<'EOF'
id,region,revenue
1,west,29.97
2,east,19.99
3,west,69.93
4,north,12.50
5,east,44.00
EOF
```

Any CSV with a header row works; so do TSV, NDJSON, JSON arrays,
Parquet, Arrow IPC, Excel and SPSS ([`import`](../cli/reference.md)).

## 2. Import it

```bash
pulse import csv --input sales.csv --output sales.pulse
```

A **cohort** is one `.pulse` file: the rows plus the schema that
describes them. Pulse samples rows to infer each column's type, and
stores a string column with few distinct values (`region`) as a
**dictionary**: each distinct string once, each row a small code
pointing at it.

To choose types and write field descriptions yourself, write a template,
edit it, and import with it:

```bash
pulse import schema-template sales.csv > sales.schema.json
# edit sales.schema.json (below), then:
pulse import csv --input sales.csv --schema sales.schema.json --output sales.pulse
```

The template lists each column with its inferred type and an empty
description. Fill in what each column means: agents read the
descriptions to pick fields, and `pulse api predict` warns while one is
empty. One entry, before and after:

```json
{"name": "revenue", "type": "f64", "description": "", "nullable": false}
{"name": "revenue", "type": "f64", "description": "Order revenue in US dollars", "nullable": false}
```

The inferred types here are `u4` (a whole number 0–15, enough for these
ids; change it to `u32` when ids run higher), `categorical_u8` (a
dictionary string with up to 256 distinct values) and `f64` (a decimal
number). Every type: [Field Types](../format/field-types.md).

## 3. Inspect it

The file carries its schema in its header, so inspect never reads
records:

```bash
pulse cohort inspect sales.pulse
pulse cohort inspect sales.pulse --json
```

The JSON form is described in
[`pulse cohort inspect`](../cli/cohort-inspect.md).

## Next

[Your First Analysis](first-analysis.md) runs a request against
`sales.pulse`.
