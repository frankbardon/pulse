# Label, Range and Lookup Tables

**Audience:** embedders giving requests named reference data: display
names for codes, named bands, keyed rates.

| Table | Holds | A request uses it through |
|---|---|---|
| Label (`pulse.LabelTable`) | key → display string | a `labels` binding (display only: filters and group keys still see the raw value), `pulse_label_resolve` for reverse search |
| Range (`pulse.RangeTable`) | named `{label, start, end}` bands, all dates or all numbers | `params.table` on `GROUP_DATE_RANGES`, `GROUP_NUMERIC_RANGES` and `FILTER_DATE_RANGES`; `range_label()` in an expression |
| Lookup (`pulse.LookupTable`) | joined key → number | `lookup("table", key…)` in an expression |

## When to use them

Use a table when the same mapping recurs across requests or must stay
out of the cohort: a code list that changes without a re-import, score
bands shared by every report, a rate grid. Inline `ranges` or an
expression literal is simpler for a one-off.

## Registering

Two sources, one name space per kind (a name declared twice is
`PULSE_EXTENSION_DUPLICATE`):

- **In code:** `Options.Extensions.LabelTables`, `RangeTables`,
  `LookupTables`, maps keyed by table name. A label or lookup table
  takes `Rows`, or a `Lookup` func that reads an external store.
- **From a directory:** every `*.json` file in `Options.LabelTablesDir`,
  `RangeTablesDir` or `LookupTablesDir` (else `PULSE_LABEL_TABLES_DIR`,
  `PULSE_RANGE_TABLES_DIR`, `PULSE_LOOKUP_TABLES_DIR`) loads at
  `pulse.New`, named by its file stem. These directories are read from
  the OS filesystem, never `Options.FS`; a missing directory loads
  nothing, a malformed file fails `pulse.New` naming the path. Validate
  a lookup file the way the loader does with `pulse.ParseLookupTableFile`.

```go compile
package tables

import "github.com/frankbardon/pulse"

func New(dataDir string) (*pulse.Pulse, error) {
	start, mid := 0.0, 50.0
	return pulse.New(pulse.Options{DataDir: dataDir, Extensions: pulse.Extensions{
		LabelTables: map[string]pulse.LabelTable{
			"region_names": {Rows: map[string]string{"n": "North", "s": "South"}},
		},
		RangeTables: map[string]pulse.RangeTable{
			"score_bands": {NumericRanges: []pulse.NumericRangeSpec{
				{Label: "low", Start: &start, End: &mid},
				{Label: "high", Start: &mid},
			}},
		},
		LookupTables: map[string]pulse.LookupTable{
			"uplift": {Rows: map[string]float64{"North|low": 1.05, "North|high": 1.10}},
		},
	}})
}
```

`Pulse.LabelTables()`, `RangeTables()` and `LookupTables()` list what
an instance carries; agents see the same lists through the
`pulse_label_tables`, `pulse_range_tables` and `pulse_lookup_tables`
tools ([MCP Integration](../mcp/index.md#tool-surface)). A feature
profile hides each kind with `capability:labels`,
`capability:range_tables` or `capability:lookup_tables`.

## Where the detail lives

- File formats and validation rules:
  [Extension Points → Lookup tables](../internals/extension-points.md#lookup-tables)
  and the GoDoc of each type.
- Labels at output time: the `label-display` skill
  (`pulse skills show label-display`); range groupers:
  [Choosing an Operator](operators.md); `lookup()` and `range_label()`:
  the `lookup-formulas` skill.
- GoDoc: [`LabelTable`](https://pkg.go.dev/github.com/frankbardon/pulse#LabelTable),
  [`RangeTable`](https://pkg.go.dev/github.com/frankbardon/pulse#RangeTable),
  [`LookupTable`](https://pkg.go.dev/github.com/frankbardon/pulse#LookupTable),
  [`ParseLookupTableFile`](https://pkg.go.dev/github.com/frankbardon/pulse#ParseLookupTableFile).
