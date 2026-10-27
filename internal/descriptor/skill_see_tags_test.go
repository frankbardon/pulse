package descriptor

import (
	"testing"

	"github.com/frankbardon/pulse/internal/examples"
	"github.com/frankbardon/pulse/internal/skills"
)

// TestSkillSeeExampleTagsResolve pins that every `tags=[...]` search a
// skill's `## See` section names matches at least one shipped example.
// A tag search matching nothing is a dead line served verbatim to the
// reader.
func TestSkillSeeExampleTagsResolve(t *testing.T) {
	t.Parallel()

	lib := examples.All()
	resolves := func(search seeTagSearch) bool {
		for _, ex := range lib {
			if search.matches(ex.Tags) {
				return true
			}
		}
		return false
	}
	for _, md := range skills.List() {
		body, ok := skills.Get(md.Name)
		if !ok {
			continue
		}
		for _, span := range backtickSpans(seeSection(body)) {
			searches, _ := seeTags(span)
			for _, search := range searches {
				if !resolves(search) {
					t.Errorf("skill %q: `## See` span %q matches no example", md.Name, span)
				}
			}
		}
	}
}
