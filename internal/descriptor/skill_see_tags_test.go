package descriptor

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/internal/examples"
	"github.com/frankbardon/pulse/internal/skills"
)

// seeTagSets returns every tag set a `## See` span names: each
// `tags=[...]` list is an AND of comma-separated entries, an entry may
// offer `a|b` alternatives (one set per combination is overkill; an
// alternative list resolves when every entry has some resolvable
// alternative, checked per entry by the caller via the returned
// groups). Quotes around a tag are author formatting.
func seeTagGroups(span string) [][][]string {
	var out [][][]string
	for {
		i := strings.Index(span, "tags=[")
		if i < 0 {
			return out
		}
		rest := span[i+len("tags=["):]
		j := strings.IndexByte(rest, ']')
		if j < 0 {
			return out
		}
		var group [][]string
		for _, ent := range strings.Split(rest[:j], ",") {
			var alts []string
			for _, a := range strings.Split(ent, "|") {
				if a = strings.Trim(strings.TrimSpace(a), `"'`); a != "" {
					alts = append(alts, a)
				}
			}
			if len(alts) > 0 {
				group = append(group, alts)
			}
		}
		if len(group) > 0 {
			out = append(out, group)
		}
		span = rest[j:]
	}
}

// TestSkillSeeExampleTagsResolve pins that every `tags=[...]` search a
// skill's `## See` section names matches at least one shipped example.
// A tag search matching nothing is a dead line served verbatim to the
// reader.
func TestSkillSeeExampleTagsResolve(t *testing.T) {
	lib := examples.All()
	// Each entry of a group must be carried by one example; for `a|b`
	// entries any alternative will do.
	resolves := func(group [][]string) bool {
		for _, ex := range lib {
			ok := true
			for _, alts := range group {
				hit := false
				for _, a := range alts {
					if hasAllTags(ex.Tags, []string{a}) {
						hit = true
						break
					}
				}
				if !hit {
					ok = false
					break
				}
			}
			if ok {
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
			for _, g := range seeTagGroups(span) {
				if !resolves(g) {
					t.Errorf("skill %q: `## See` span %q matches no example", md.Name, span)
				}
			}
		}
	}
}
