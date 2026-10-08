package pulse

import (
	"os"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/observe"
)

// TestObservabilityDocCoversEnums keeps .claude/reference/observability.md
// honest: every OperationKind, Phase, Arm, Scope and metric name in code
// must appear in it. Falsified by adding an enum value without a doc edit.
func TestObservabilityDocCoversEnums(t *testing.T) {
	b, err := os.ReadFile(".claude/reference/observability.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	need := func(kind, v string) {
		if !strings.Contains(doc, "`"+v+"`") {
			t.Errorf("observability.md does not mention %s `%s`", kind, v)
		}
	}
	for _, k := range observe.AllOperationKinds() {
		need("operation kind", string(k))
	}
	for _, v := range observe.AllPhases() {
		need("phase", string(v))
	}
	for _, v := range observe.AllArms() {
		need("arm", string(v))
	}
	for _, v := range observe.AllScopes() {
		need("scope", string(v))
	}
	for _, m := range metricNames {
		need("metric", m)
	}
}
