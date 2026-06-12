package arrow

import (
	"encoding/json"
	"fmt"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"

	"github.com/frankbardon/pulse/types"
)

// jsonMarshalOverlay centralises encoding/json marshalling of overlay
// sub-payloads (Ref / Payload / Summary) so the Writer path and the
// helper builders share one error surface. All overlay JSON in the
// Arrow embedding goes through encoding/json per CLAUDE.md "Structural
// defense bans".
func jsonMarshalOverlay(v any) ([]byte, error) {
	return json.Marshal(v)
}

// OverlaysFieldName is the top-level Arrow field name that carries an
// overlay sidecar column family. Reserved namespace — pulse cohort
// schemas never produce a field with this name (schema.Field.Name is
// validated against the source schema, which does not allow synthesised
// names with this label).
//
// Per research/export-embedding-shape.md § 3.1, the field rides at the
// SAME nesting level as the host record fields (top-level of the Arrow
// Schema). It is structurally absent (the field is OMITTED, not present
// with-empty-list) when IncludeOverlays resolves to false OR when
// Response.Overlays is empty.
const OverlaysFieldName = "overlays"

// overlaysFieldType returns the fixed Arrow `LIST<STRUCT>` shape used
// for every overlay column family per research/export-embedding-shape.md
// § 3.1. The struct schema is FIXED — every Arrow writer emits the SAME
// nested struct schema regardless of which overlay kinds were produced
// so downstream Arrow consumers (DuckDB, polars, Spark) can register the
// schema once and reuse it.
//
// The nested payload arms (scalar / series / matrix) are present in the
// struct with NULL cells for absent arms — exactly one arm is populated
// per layer per the OverlayPayload.Shape discriminator. The dispatch is
// inline on `shape` (UTF8).
func overlaysFieldType() *arrow.StructType {
	return arrow.StructOf(
		arrow.Field{Name: "name", Type: arrow.BinaryTypes.String, Nullable: false},
		arrow.Field{Name: "kind", Type: arrow.BinaryTypes.String, Nullable: false},
		arrow.Field{Name: "scope", Type: arrow.BinaryTypes.String, Nullable: false},
		// ref JSON-marshalled via encoding/json (no fmt.Sprintf-built
		// JSON per CLAUDE.md "Structural defense bans"). The reader
		// unmarshals back into types.OverlayRef on round-trip.
		arrow.Field{Name: "ref", Type: arrow.BinaryTypes.String, Nullable: false},
		arrow.Field{Name: "shape", Type: arrow.BinaryTypes.String, Nullable: false},
		// payload + summary land as JSON strings rather than nested
		// Arrow structs. Per the universal wire-shape principles
		// (research/export-embedding-shape.md § 2 rule 3) the
		// discriminator stays inline; the payload body rides as a JSON
		// blob so we round-trip every OverlayPayload arm
		// (scalar/series/matrix) byte-equivalently without per-arm
		// Arrow schema. The "nested struct schema is FIXED" guarantee
		// (§ 3.4) is preserved because the FIXED schema lives on the
		// outer struct — the payload JSON is uniform across kinds.
		arrow.Field{Name: "payload", Type: arrow.BinaryTypes.String, Nullable: true},
		arrow.Field{Name: "summary", Type: arrow.BinaryTypes.String, Nullable: true},
	)
}

// overlaysArrowField returns the top-level Arrow Field carrying the
// overlay column family. Caller appends this to the schema when
// overlays should be embedded.
func overlaysArrowField() arrow.Field {
	return arrow.Field{
		Name:     OverlaysFieldName,
		Type:     arrow.ListOf(overlaysFieldType()),
		Nullable: true,
	}
}

// readOverlaysFromArray walks the LIST<STRUCT> overlay array and
// reconstructs []*types.OverlayLayer. The reader scans every non-empty
// list element across every record-batch and returns the first non-
// empty list (the canonical writer emits the populated list in row 0 of
// batch 0; reading any earlier row is sufficient). Returns nil when
// the array is structurally absent or every list element is empty —
// nil signals "no overlays present" to callers.
func readOverlaysFromArray(listArr arrow.Array) ([]*types.OverlayLayer, error) {
	if listArr == nil {
		return nil, nil
	}
	la, ok := listArr.(*array.List)
	if !ok {
		return nil, fmt.Errorf("arrow.overlay: expected list array, got %T", listArr)
	}
	if la.Len() == 0 {
		return nil, nil
	}

	values := la.ListValues()
	structArr, ok := values.(*array.Struct)
	if !ok {
		return nil, fmt.Errorf("arrow.overlay: expected struct values, got %T", values)
	}

	// Walk each row's list element; return the first non-empty list.
	for r := 0; r < la.Len(); r++ {
		if la.IsNull(r) {
			continue
		}
		offsets := la.Offsets()
		start := int(offsets[r])
		end := int(offsets[r+1])
		if end <= start {
			continue
		}
		layers := make([]*types.OverlayLayer, 0, end-start)
		for i := start; i < end; i++ {
			if structArr.IsNull(i) {
				layers = append(layers, nil)
				continue
			}
			layer, err := decodeOverlayStruct(structArr, i)
			if err != nil {
				return nil, err
			}
			layers = append(layers, layer)
		}
		return layers, nil
	}
	return nil, nil
}

// decodeOverlayStruct rebuilds one OverlayLayer from row i of the
// overlay struct array. Mirrors the field order in overlaysFieldType.
func decodeOverlayStruct(s *array.Struct, i int) (*types.OverlayLayer, error) {
	nameCol := s.Field(0).(*array.String)
	kindCol := s.Field(1).(*array.String)
	scopeCol := s.Field(2).(*array.String)
	refCol := s.Field(3).(*array.String)
	shapeCol := s.Field(4).(*array.String)
	payloadCol := s.Field(5).(*array.String)
	summaryCol := s.Field(6).(*array.String)

	layer := &types.OverlayLayer{
		Name:  nameCol.Value(i),
		Kind:  types.OverlayKind(kindCol.Value(i)),
		Scope: types.OverlayScope(scopeCol.Value(i)),
	}
	if !refCol.IsNull(i) {
		if err := json.Unmarshal([]byte(refCol.Value(i)), &layer.Ref); err != nil {
			return nil, fmt.Errorf("arrow.overlay: unmarshal ref: %w", err)
		}
	}
	if !payloadCol.IsNull(i) {
		if err := json.Unmarshal([]byte(payloadCol.Value(i)), &layer.Payload); err != nil {
			return nil, fmt.Errorf("arrow.overlay: unmarshal payload: %w", err)
		}
	}
	// Defensive: ensure Payload.Shape matches the inline discriminator
	// even when the payload JSON was empty (e.g. malformed input).
	layer.Payload.Shape = types.OverlayShape(shapeCol.Value(i))
	if !summaryCol.IsNull(i) {
		var sum types.OverlaySummary
		if err := json.Unmarshal([]byte(summaryCol.Value(i)), &sum); err != nil {
			return nil, fmt.Errorf("arrow.overlay: unmarshal summary: %w", err)
		}
		layer.Summary = &sum
	}
	return layer, nil
}
