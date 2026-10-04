package gosdk

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestJSONResult_UndefinedFigureIsNull: a tool result holding a
// non-finite float (a 0/0 AGG_RATIO, an undefined overlay entry) is a
// successful result whose JSON says null there — never an
// "encode result" tool error.
func TestJSONResult_UndefinedFigureIsNull(t *testing.T) {
	res, err := jsonResult(map[string]any{"data": []map[string]any{{"r": math.NaN(), "s": math.Inf(1), "n": 2}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %v", res.Content)
	}
	text := res.Content[0].(*mcpsdk.TextContent).Text
	if !json.Valid([]byte(text)) || !strings.Contains(text, `"r":null`) || !strings.Contains(text, `"s":null`) {
		t.Errorf("result %s, want valid JSON with r and s null", text)
	}
}
