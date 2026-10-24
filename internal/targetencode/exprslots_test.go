package targetencode_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/exprslots"
	"github.com/frankbardon/pulse/internal/targetencode"
)

// TestTemplateExprSplice_FeatureFitOnSlot: FEAT_TARGET_ENCODE's
// params.fit_on is a row expression the engine compiles as an
// expression filter, so the shared expression-slot list
// (internal/exprslots) names its key: a `{{token}}` spliced into it —
// by a template, a sweep or a for_each axis — is refused. Renaming the
// wire key without the slot fails here.
func TestTemplateExprSplice_FeatureFitOnSlot(t *testing.T) {
	f, ok := reflect.TypeFor[targetencode.Params]().FieldByName("FitOn")
	if !ok || f.Type.Kind() != reflect.String {
		t.Fatal("targetencode.Params.FitOn is gone or no longer a string; update internal/exprslots")
	}
	key := strings.Split(f.Tag.Get("json"), ",")[0]
	if key != exprslots.FeatureFitOnParam {
		t.Fatalf("params key %q, exprslots.FeatureFitOnParam %q", key, exprslots.FeatureFitOnParam)
	}
	for _, keys := range [][]string{
		{"features", "params", key},
		{"requests", "features", "params", key},
		{"stages", "request", "features", "params", key},
	} {
		if s, ok := exprslots.Match(keys); !ok || s.Name != exprslots.SlotFeatureFitOn {
			t.Errorf("Match(%q) = (%q, %v), want %s", keys, s.Name, ok, exprslots.SlotFeatureFitOn)
		}
	}
}
