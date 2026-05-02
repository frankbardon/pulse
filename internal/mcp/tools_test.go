package mcp

import (
	"slices"
	"testing"
)

func TestRegisteredTools_Stable(t *testing.T) {
	got := RegisteredTools()
	want := []string{
		ToolInspect,
		ToolPredict,
		ToolProcess,
		ToolCompose,
		ToolSample,
		ToolFacet,
		ToolSkillsList,
		ToolSkillsGet,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("RegisteredTools mismatch\n got: %v\nwant: %v", got, want)
	}
}

func TestRegisteredTools_AllPrefixed(t *testing.T) {
	for _, name := range RegisteredTools() {
		if len(name) < 6 || name[:6] != "pulse_" {
			t.Errorf("tool %q must use pulse_ prefix", name)
		}
	}
}
