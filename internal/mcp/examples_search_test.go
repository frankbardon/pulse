package mcp

import (
	"context"
	stderrors "errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/examples"
	"github.com/frankbardon/pulse/internal/mcp/toolmeta"
)

// TestExamplesSearch_IntentArg: the intent argument filters, and an
// unknown one is the coded recommend error, not a stringified one.
func TestExamplesSearch_IntentArg(t *testing.T) {
	p := defaultPulse(t)
	out, err := HandleExamplesSearch(context.Background(), p, ExamplesSearchIn{Intent: "relationship"})
	if err != nil || len(out.Results) == 0 {
		t.Fatalf("intent filter: %d results, err %v", len(out.Results), err)
	}
	for _, r := range out.Results {
		if !slices.Contains(r.Intents, "relationship") {
			t.Errorf("%s lacks relationship", r.Name)
		}
	}
	_, err = HandleExamplesSearch(context.Background(), p, ExamplesSearchIn{Intent: "nope"})
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_RECOMMEND_INTENT_UNKNOWN {
		t.Fatalf("unknown intent: err = %v", err)
	}
}

// TestExamplesSearch_CategoryEnumCurrent: the input-schema description
// and the tool description name every library category, so the enum
// cannot drift from the embedded directories again.
func TestExamplesSearch_CategoryEnumCurrent(t *testing.T) {
	f, _ := reflect.TypeOf(ExamplesSearchIn{}).FieldByName("Category")
	schema := f.Tag.Get("jsonschema")
	for _, c := range examples.AllCategories() {
		if !strings.Contains(schema, c) {
			t.Errorf("ExamplesSearchIn.category description omits %q", c)
		}
		if !strings.Contains(toolmeta.DescExamplesSearch, "`"+c+"`") {
			t.Errorf("DescExamplesSearch omits category %q", c)
		}
	}
}
