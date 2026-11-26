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

Pulse samples rows to infer each column's type and builds a dictionary
for low-cardinality strings. To choose types and write field
descriptions yourself, edit a template and import with it:

```bash
pulse import schema-template sales.csv > sales.schema.json
# edit sales.schema.json: types, descriptions
pulse import csv --input sales.csv --schema sales.schema.json --output sales.pulse
```

The type catalog is [Field Types](../format/field-types.md).

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
