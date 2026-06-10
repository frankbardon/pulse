package service

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// BenchmarkBufferedProcessWideCohort is the in-tree, hermetic equivalent of
// the canonical user-facing benchmark in `tmp/huge-request.json` × the
// (external-drive) `vc/1.pulse` cohort. The cohort lives on an external
// volume and is not portable across machines or CI, so this bench
// synthesises a 200-field × 100K-row cohort with a field-type mix similar
// to the real `vc/1.pulse` (categorical-heavy + a few numeric/date/decimal
// fields plus one set_u8, one packed_bool, and one nullable categorical so
// subsequent epics' decode-plan goldens have something to chew on).
//
// The driven request mirrors `tmp/huge-request.json`:
//   - rows:    [{ GROUP_CATEGORY field=brand }]
//   - columns: [
//     { GROUP_DATE field=waveDate component=quarter fiscal_offset=-3 },
//     { GROUP_CATEGORY field=cardFeeling },
//     ]
//   - cell:    AGG_SUM(weight) label=sum_weight
//   - margins: rows, columns, grand
//   - shape:   matrix (default)
//   - normalize: row
//   - normalize_within: 0  (fixes the outer column grouper as the denominator slab)
//
// This bench is the standing measurement surface for the crosstab-perf
// epic — E2-S4, E3-S5, and E4-S5 will compare deltas against the baseline
// captured here. No numerical perf assertion lives in this story; it just
// stands up the harness.
func BenchmarkBufferedProcessWideCohort(b *testing.B) {
	const (
		fieldCount = 200
		rowCount   = 100_000
	)

	dir := b.TempDir()
	osFs := afero.NewOsFs()
	path := dir + "/bench_wide.pulse"

	schema, payload := buildWideCohort(b, fieldCount, rowCount)
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		b.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		b.Fatalf("WriteSchema: %v", err)
	}
	buf.Write(payload)
	if err := afero.WriteFile(osFs, path, buf.Bytes(), 0644); err != nil {
		b.Fatalf("WriteFile: %v", err)
	}

	cfg, _ := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	svc := New(cfg)

	fiscalOffset := -3
	req := &types.Request{
		Cohort: &types.Cohort{Filename: path},
		Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "brand"},
			},
			Columns: []*types.Group{
				{
					Type:   types.GROUP_DATE,
					Field:  "waveDate",
					Params: dateParams("quarter", &fiscalOffset),
				},
				{Type: types.GROUP_CATEGORY, Field: "cardFeeling"},
			},
			Cell: &types.Aggregation{
				Type:  types.AGG_SUM,
				Field: "weight",
				Label: "sum_weight",
			},
			Margins:         types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			Shape:           types.CrosstabShapeMatrix,
			Normalize:       types.CrosstabNormalizeRow,
			NormalizeWithin: intPtr(0),
		},
	}

	// Sanity-check the request once before the timed loop so a bench
	// failure surfaces as a real error, not a timed-out hang.
	ctx := context.Background()
	if _, err := svc.Process(ctx, req); err != nil {
		b.Fatalf("warmup Process: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := svc.Process(ctx, req); err != nil {
			b.Fatalf("Process: %v", err)
		}
	}
}

// dateParams renders a GROUP_DATE params JSON blob with the given
// component and optional fiscal_offset.
func dateParams(component string, fiscalOffset *int) []byte {
	if fiscalOffset == nil {
		return []byte(fmt.Sprintf(`{"component":%q}`, component))
	}
	return []byte(fmt.Sprintf(`{"component":%q,"fiscal_offset":%d}`, component, *fiscalOffset))
}

func intPtr(v int) *int { return &v }

// buildWideCohort constructs a (schema, packed payload) pair with a 200-field
// × N-row layout that mirrors vc/1.pulse's shape: a handful of named fields
// driving the crosstab request plus a padded tail of mixed-type fields
// representing the long categorical tail of the real cohort. Includes at
// least one decimal128, one set_u8, one packed_bool, and one nullable
// categorical for downstream epics' decode-plan goldens.
func buildWideCohort(b *testing.B, fieldCount, rowCount int) (*encoding.Schema, []byte) {
	b.Helper()

	// Named-field dictionaries.
	brandDict := encoding.NewDictionary()
	const brandCard = 16
	for i := range brandCard {
		if _, err := brandDict.Add(fmt.Sprintf("brand_%02d", i)); err != nil {
			b.Fatalf("brand dict: %v", err)
		}
	}
	feelingDict := encoding.NewDictionary()
	const feelingCard = 8
	for i := range feelingCard {
		if _, err := feelingDict.Add(fmt.Sprintf("feel_%d", i)); err != nil {
			b.Fatalf("feeling dict: %v", err)
		}
	}

	// Set field dictionary (issuer list, set_u8 — up to 8 entries).
	setDict := encoding.NewDictionary()
	for i := range 6 {
		if _, err := setDict.Add(fmt.Sprintf("iss_%d", i)); err != nil {
			b.Fatalf("set dict: %v", err)
		}
	}

	// Nullable categorical (a tail field). 4 categories.
	nullCatDict := encoding.NewDictionary()
	for i := range 4 {
		if _, err := nullCatDict.Add(fmt.Sprintf("opt_%d", i)); err != nil {
			b.Fatalf("nullcat dict: %v", err)
		}
	}

	// Pre-build the categorical_u8 dictionaries for the wide pad. Each pad
	// dict has 12 entries to keep dict-resolve cost realistic.
	padCatDicts := make([]*encoding.Dictionary, 0)

	// Layout: lay down the named fields first so they sit at predictable
	// byte offsets, then pad with mixed types up to fieldCount. Offsets
	// are tracked as we build.
	fields := make([]encoding.Field, 0, fieldCount)
	byteOffset := 0
	add := func(f encoding.Field) {
		f.ByteOffset = byteOffset
		if f.Type.IsBitPacked() {
			byteOffset++
		} else {
			byteOffset += f.Type.ByteSize()
		}
		f.CsvColumnIdx = len(fields)
		fields = append(fields, f)
	}

	// Named fields driving the crosstab request.
	add(encoding.Field{Name: "brand", Type: encoding.FieldTypeCategoricalU8, Dictionary: brandDict})
	add(encoding.Field{Name: "waveDate", Type: encoding.FieldTypeDate})
	add(encoding.Field{Name: "cardFeeling", Type: encoding.FieldTypeCategoricalU8, Dictionary: feelingDict})
	add(encoding.Field{Name: "weight", Type: encoding.FieldTypeF64})

	// Required diverse-type witnesses for downstream goldens.
	add(encoding.Field{
		Name:      "amount",
		Type:      encoding.FieldTypeDecimal128,
		Precision: 18,
		Scale:     2,
	})
	add(encoding.Field{Name: "issuers", Type: encoding.FieldTypeSetU8, Dictionary: setDict})
	add(encoding.Field{Name: "active", Type: encoding.FieldTypePackedBool})
	add(encoding.Field{
		Name:       "tier",
		Type:       encoding.FieldTypeCategoricalU8,
		Dictionary: nullCatDict,
		Nullable:   true,
	})

	// Pad with a deterministic mix of types until we hit fieldCount.
	// Mix mirrors vc/1.pulse's ratio: ~70% categorical_u8, ~10% f64,
	// ~10% u32, ~5% u16, ~5% f32. (Approximate; tuned to keep record
	// stride and dict-resolve cost realistic without dominating.)
	padIdx := 0
	for len(fields) < fieldCount {
		var f encoding.Field
		f.Name = fmt.Sprintf("pad_%03d", padIdx)
		switch padIdx % 20 {
		case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13: // 70% categorical_u8
			dict := encoding.NewDictionary()
			for i := range 12 {
				if _, err := dict.Add(fmt.Sprintf("v%02d_%d", padIdx%50, i)); err != nil {
					b.Fatalf("pad cat dict: %v", err)
				}
			}
			padCatDicts = append(padCatDicts, dict)
			f.Type = encoding.FieldTypeCategoricalU8
			f.Dictionary = dict
		case 14, 15: // 10% f64
			f.Type = encoding.FieldTypeF64
		case 16, 17: // 10% u32
			f.Type = encoding.FieldTypeU32
		case 18: // 5% u16
			f.Type = encoding.FieldTypeU16
		case 19: // 5% f32
			f.Type = encoding.FieldTypeF32
		}
		add(f)
		padIdx++
	}

	schema := &encoding.Schema{Fields: fields}
	bitmapSize := schema.BitmapByteSize()

	// Materialize records. Cycle deterministically so the bench is
	// reproducible. Reserve estimate avoids resize-during-write cost
	// from showing up in the bench (resize happens before ResetTimer).
	stride := schema.RecordByteSize()
	out := bytes.NewBuffer(make([]byte, 0, rowCount*stride))

	// Date range: 2020-01-01..2024-12-31 (~5 years) so the fiscal
	// quarter grouper has ~20 buckets to populate.
	epoch := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
	startDays := int64(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).Sub(epoch).Hours() / 24)
	const daySpan = 365 * 5

	for r := range rowCount {
		// brand: cycle over 16 brands
		if err := encoding.WriteFieldValue(out, encoding.FieldTypeCategoricalU8, uint64(r%brandCard)); err != nil {
			b.Fatalf("write brand: %v", err)
		}
		// waveDate: monotonic-with-noise across the date span
		days := startDays + int64((r*7)%daySpan)
		if err := encoding.WriteFieldValue(out, encoding.FieldTypeDate, uint64(days)); err != nil {
			b.Fatalf("write date: %v", err)
		}
		// cardFeeling: cycle over 8
		if err := encoding.WriteFieldValue(out, encoding.FieldTypeCategoricalU8, uint64((r*3)%feelingCard)); err != nil {
			b.Fatalf("write feeling: %v", err)
		}
		// weight: deterministic non-trivial float
		w := float64((r*37)%1009) + 0.5
		if err := encoding.WriteFieldValue(out, encoding.FieldTypeF64, math.Float64bits(w)); err != nil {
			b.Fatalf("write weight: %v", err)
		}
		// amount: decimal128 (scale=2). Encode a non-zero integer mantissa
		// derived from r so the field is non-degenerate.
		dec := encoding.NewDecimal128FromInt(int64((r*131)%9973) + 1)
		if err := encoding.WriteDecimal128(out, dec); err != nil {
			b.Fatalf("write decimal: %v", err)
		}
		// issuers: set_u8 — rotating bitmask over the 6-entry dict.
		mask := uint8(1<<(r%6) | 1<<((r+2)%6))
		if err := encoding.WriteFieldValue(out, encoding.FieldTypeSetU8, uint64(mask)); err != nil {
			b.Fatalf("write set: %v", err)
		}
		// active: packed_bool — alternating
		var activeByte byte
		if r%2 == 0 {
			activeByte = 1
		}
		if _, err := out.Write([]byte{activeByte}); err != nil {
			b.Fatalf("write packed_bool: %v", err)
		}
		// tier: nullable categorical_u8. Write a dict index regardless;
		// nullability is signalled via the trailing bitmap.
		if err := encoding.WriteFieldValue(out, encoding.FieldTypeCategoricalU8, uint64(r%4)); err != nil {
			b.Fatalf("write tier: %v", err)
		}

		// Pad tail (fields 8..fieldCount-1).
		padCatIdx := 0
		for i := 8; i < len(fields); i++ {
			f := fields[i]
			switch f.Type {
			case encoding.FieldTypeCategoricalU8:
				dict := padCatDicts[padCatIdx]
				padCatIdx++
				size := len(dict.Values())
				if size == 0 {
					size = 1
				}
				if err := encoding.WriteFieldValue(out, encoding.FieldTypeCategoricalU8, uint64((r+i)%size)); err != nil {
					b.Fatalf("write pad cat: %v", err)
				}
			case encoding.FieldTypeF64:
				v := float64((r*i)%509) + 0.25
				if err := encoding.WriteFieldValue(out, encoding.FieldTypeF64, math.Float64bits(v)); err != nil {
					b.Fatalf("write pad f64: %v", err)
				}
			case encoding.FieldTypeU32:
				if err := encoding.WriteFieldValue(out, encoding.FieldTypeU32, uint64((r*7+i)%1000000)); err != nil {
					b.Fatalf("write pad u32: %v", err)
				}
			case encoding.FieldTypeU16:
				if err := encoding.WriteFieldValue(out, encoding.FieldTypeU16, uint64((r+i)%4096)); err != nil {
					b.Fatalf("write pad u16: %v", err)
				}
			case encoding.FieldTypeF32:
				v := float32((r*i)%509) + 0.5
				if err := encoding.WriteFieldValue(out, encoding.FieldTypeF32, uint64(math.Float32bits(v))); err != nil {
					b.Fatalf("write pad f32: %v", err)
				}
			default:
				b.Fatalf("unexpected pad type %s", f.Type)
			}
		}

		// Null bitmap trailer: one nullable field ("tier") at index 7.
		// Mark ~10% of rows null.
		if bitmapSize > 0 {
			bitmap := make([]byte, bitmapSize)
			if r%10 == 0 {
				encoding.BitmapSetNull(bitmap, 7)
			}
			if err := encoding.WriteBitmap(out, bitmap); err != nil {
				b.Fatalf("write bitmap: %v", err)
			}
		}
	}

	return schema, out.Bytes()
}
