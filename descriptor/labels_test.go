package descriptor

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func labelTestSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	dict := encoding.NewDictionary()
	if _, err := dict.Add("US"); err != nil {
		t.Fatal(err)
	}
	if _, err := dict.Add("CA"); err != nil {
		t.Fatal(err)
	}
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "amount", Type: encoding.FieldTypeF64, ByteOffset: 0},
			{Name: "country", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 8, Dictionary: dict},
		},
	}
}

func snapshotWith(names ...string) *ExtensionsSnapshot {
	out := &ExtensionsSnapshot{}
	for _, n := range names {
		out.LabelTables = append(out.LabelTables, LabelTableMeta{Name: n, HasRowsData: true})
	}
	return out
}

func envHasCode(env *Envelope, code errors.Code) bool {
	for _, e := range env.Errors {
		if e.Code == string(code) {
			return true
		}
	}
	return false
}

func TestValidateLabels_OKReplace(t *testing.T) {
	env := NewEnvelope(nil)
	ValidateLabels(env,
		[]*types.LabelBinding{{Field: "country", Table: "country_names", Mode: types.LabelModeReplace}},
		labelTestSchema(t),
		snapshotWith("country_names"),
		nil,
	)
	if len(env.Errors) > 0 {
		t.Fatalf("unexpected errors: %+v", env.Errors)
	}
}

func TestValidateLabels_OKAugment(t *testing.T) {
	env := NewEnvelope(nil)
	aug := ValidateLabels(env,
		[]*types.LabelBinding{{Field: "country", Table: "country_names", Mode: types.LabelModeAugment}},
		labelTestSchema(t),
		snapshotWith("country_names"),
		nil,
	)
	if len(env.Errors) > 0 {
		t.Fatalf("unexpected errors: %+v", env.Errors)
	}
	if !aug["country_label"] {
		t.Fatalf("expected augment sibling country_label, got %v", aug)
	}
}

func TestValidateLabels_UnknownField(t *testing.T) {
	env := NewEnvelope(nil)
	ValidateLabels(env,
		[]*types.LabelBinding{{Field: "missing", Table: "country_names"}},
		labelTestSchema(t),
		snapshotWith("country_names"),
		nil,
	)
	if !envHasCode(env, errors.PULSE_LABEL_FIELD_UNKNOWN) {
		t.Fatalf("expected PULSE_LABEL_FIELD_UNKNOWN, got %+v", env.Errors)
	}
}

func TestValidateLabels_NonCategoricalField(t *testing.T) {
	env := NewEnvelope(nil)
	ValidateLabels(env,
		[]*types.LabelBinding{{Field: "amount", Table: "country_names"}},
		labelTestSchema(t),
		snapshotWith("country_names"),
		nil,
	)
	if !envHasCode(env, errors.PULSE_LABEL_FIELD_NOT_CATEGORICAL) {
		t.Fatalf("expected PULSE_LABEL_FIELD_NOT_CATEGORICAL, got %+v", env.Errors)
	}
}

func TestValidateLabels_UnknownTable(t *testing.T) {
	env := NewEnvelope(nil)
	ValidateLabels(env,
		[]*types.LabelBinding{{Field: "country", Table: "missing_table"}},
		labelTestSchema(t),
		snapshotWith("country_names"),
		nil,
	)
	if !envHasCode(env, errors.PULSE_LABEL_TABLE_UNKNOWN) {
		t.Fatalf("expected PULSE_LABEL_TABLE_UNKNOWN, got %+v", env.Errors)
	}
}

func TestValidateLabels_NilSnapshotRejects(t *testing.T) {
	env := NewEnvelope(nil)
	ValidateLabels(env,
		[]*types.LabelBinding{{Field: "country", Table: "country_names"}},
		labelTestSchema(t),
		nil,
		nil,
	)
	if !envHasCode(env, errors.PULSE_LABEL_TABLE_UNKNOWN) {
		t.Fatalf("expected PULSE_LABEL_TABLE_UNKNOWN with nil snapshot, got %+v", env.Errors)
	}
}

func TestValidateLabels_DuplicateField(t *testing.T) {
	env := NewEnvelope(nil)
	ValidateLabels(env,
		[]*types.LabelBinding{
			{Field: "country", Table: "country_names"},
			{Field: "country", Table: "country_names", Mode: types.LabelModeAugment},
		},
		labelTestSchema(t),
		snapshotWith("country_names"),
		nil,
	)
	if !envHasCode(env, errors.PULSE_LABEL_DUPLICATE_BINDING) {
		t.Fatalf("expected PULSE_LABEL_DUPLICATE_BINDING, got %+v", env.Errors)
	}
}

func TestValidateLabels_AugmentCollidesWithSchema(t *testing.T) {
	schema := labelTestSchema(t)
	// inject a synthetic field "country_label" that would collide.
	schema.Fields = append(schema.Fields,
		encoding.Field{Name: "country_label", Type: encoding.FieldTypeU8, ByteOffset: 16},
	)
	env := NewEnvelope(nil)
	ValidateLabels(env,
		[]*types.LabelBinding{{Field: "country", Table: "country_names", Mode: types.LabelModeAugment}},
		schema,
		snapshotWith("country_names"),
		nil,
	)
	if !envHasCode(env, errors.PULSE_LABEL_FIELD_COLLISION) {
		t.Fatalf("expected PULSE_LABEL_FIELD_COLLISION, got %+v", env.Errors)
	}
}

func TestValidateLabels_AugmentCollidesWithExtraField(t *testing.T) {
	env := NewEnvelope(nil)
	ValidateLabels(env,
		[]*types.LabelBinding{{Field: "country", Table: "country_names", Mode: types.LabelModeAugment}},
		labelTestSchema(t),
		snapshotWith("country_names"),
		map[string]bool{"country_label": true},
	)
	if !envHasCode(env, errors.PULSE_LABEL_FIELD_COLLISION) {
		t.Fatalf("expected PULSE_LABEL_FIELD_COLLISION vs extraFields, got %+v", env.Errors)
	}
}

func TestValidateLabels_InvalidMode(t *testing.T) {
	env := NewEnvelope(nil)
	ValidateLabels(env,
		[]*types.LabelBinding{{Field: "country", Table: "country_names", Mode: "highlight"}},
		labelTestSchema(t),
		snapshotWith("country_names"),
		nil,
	)
	if !envHasCode(env, errors.SERVICE_VALIDATION) {
		t.Fatalf("expected SERVICE_VALIDATION for invalid mode, got %+v", env.Errors)
	}
}

func TestValidateLabels_NilOrEmpty(t *testing.T) {
	env := NewEnvelope(nil)
	if names := ValidateLabels(env, nil, labelTestSchema(t), snapshotWith("country_names"), nil); len(names) > 0 {
		t.Fatalf("expected empty augment set for nil bindings; got %v", names)
	}
	if len(env.Errors) > 0 {
		t.Fatalf("nil bindings should not produce errors; got %+v", env.Errors)
	}
}
