package skills

import (
	"slices"
	"strings"
	"testing"
)

// TestVirtualSkillStemsNotEmbedded is the stem-collision gate: no
// embedded *.md file may claim a reserved virtual skill stem
// (glossary, intents). Adding internal/skills/glossary.md fails it.
func TestVirtualSkillStemsNotEmbedded(t *testing.T) {
	names := embeddedMarkdownNames(t)
	if !embedded("request-envelope") {
		t.Fatal("embedded() misses a real skill file: the gate would be vacuous")
	}
	for _, v := range ReservedVirtualNames() {
		if slices.Contains(names, v) || embedded(v) {
			t.Errorf("embedded skill %s.md collides with the reserved virtual skill stem %q; rename the file", v, v)
		}
	}
}

// withVirtual registers v for the test and removes it afterwards.
func withVirtual(t *testing.T, v Virtual) {
	t.Helper()
	RegisterVirtual(v)
	t.Cleanup(func() {
		virtualMu.Lock()
		delete(virtuals, v.Metadata.Name)
		virtualMu.Unlock()
	})
}

func TestRegisterVirtual_JoinsListAndGet(t *testing.T) {
	if IsVirtual(VirtualGlossary) {
		t.Skip("glossary registered by a linked package")
	}
	withVirtual(t, Virtual{
		Metadata: Metadata{Name: VirtualGlossary, Description: "d"},
		Render:   func() string { return "---\nname: glossary\n---\n\nbody\n" },
	})
	list := List()
	i := slices.IndexFunc(list, func(m Metadata) bool { return m.Name == VirtualGlossary })
	if i < 0 {
		t.Fatal("List lacks the registered virtual skill")
	}
	if list[i].Kind != KindReference {
		t.Errorf("virtual kind = %q, want %q", list[i].Kind, KindReference)
	}
	if !slices.IsSortedFunc(list, func(a, b Metadata) int { return strings.Compare(a.Name, b.Name) }) {
		t.Error("List not sorted by name with a virtual skill present")
	}
	if body, ok := Get(VirtualGlossary); !ok || !strings.HasSuffix(body, "body\n") {
		t.Errorf("Get(glossary) = %q, %v", body, ok)
	}
	if !slices.Contains(Names(), VirtualGlossary) {
		t.Error("Names lacks the virtual skill")
	}
}

func TestRegisterVirtual_Refuses(t *testing.T) {
	render := func() string { return "" }
	for name, v := range map[string]Virtual{
		"unreserved stem": {Metadata: Metadata{Name: "not-reserved"}, Render: render},
		"nil renderer":    {Metadata: Metadata{Name: VirtualIntents}},
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("RegisterVirtual(%s) did not panic", v.Metadata.Name)
				}
			}()
			RegisterVirtual(v)
		})
	}
}
