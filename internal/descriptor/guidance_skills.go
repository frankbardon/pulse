package descriptor

import (
	"sort"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/skills"
)

// The glossary and the intent taxonomy are served as two virtual skills,
// rendered from their registries here and registered with the skill
// pack so every skill surface (pulse skills list/show, pulse_skills_list
// / pulse_skills_get, pulse-skill:// resources, the manifest's skills
// list) carries them with no per-consumer wiring. Neither registry is a
// feature, so a feature profile never prunes or scrubs them.

const (
	glossarySkillDescription = "Plain-language glossary of the statistical terms Pulse guidance uses. Use when a result, purpose or skill names a term you need explained."
	intentsSkillDescription  = "The closed intent taxonomy: the kinds of question an analysis answers, how each sounds, and the field shapes it fits. Use to map a user's question to an intent ID."
)

func init() {
	skills.RegisterVirtual(skills.Virtual{
		Metadata: skills.Metadata{Name: skills.VirtualGlossary, Description: glossarySkillDescription, Type: "reference"},
		Render:   RenderGlossarySkill,
	})
	skills.RegisterVirtual(skills.Virtual{
		Metadata: skills.Metadata{Name: skills.VirtualIntents, Description: intentsSkillDescription, Type: "reference"},
		Render:   RenderIntentsSkill,
	})
}

// skillFrontmatter renders the frontmatter block a virtual skill's body
// opens with, the same shape an embedded skill file carries.
func skillFrontmatter(b *strings.Builder, name, description string) {
	b.WriteString("---\nname: ")
	b.WriteString(name)
	b.WriteString("\ndescription: ")
	b.WriteString(description)
	b.WriteString("\ntype: reference\nkind: ")
	b.WriteString(skills.KindReference)
	b.WriteString("\n---\n\n")
}

// RenderGlossarySkill renders the glossary skill's markdown: every term
// sorted by ID, with its definition, why it matters, the spellings that
// count as the term, and related terms. Deterministic.
func RenderGlossarySkill() string {
	terms := Glossary()
	sort.Slice(terms, func(i, j int) bool { return terms[i].ID < terms[j].ID })
	var b strings.Builder
	skillFrontmatter(&b, skills.VirtualGlossary, glossarySkillDescription)
	b.WriteString("# Glossary\n\n")
	b.WriteString("Plain-language definitions of the terms Pulse's guidance relies on, sorted by ID. ")
	b.WriteString("Purposes cite these IDs in their glossary lists.\n")
	for _, t := range terms {
		b.WriteString("\n## ")
		b.WriteString(t.ID)
		b.WriteString("\n\n")
		b.WriteString(t.Short)
		b.WriteString("\n")
		if t.WhyCare != "" {
			b.WriteString("\nWhy it matters: ")
			b.WriteString(t.WhyCare)
			b.WriteString("\n")
		}
		if len(t.Forms) > 0 {
			b.WriteString("\nWritten as: ")
			b.WriteString(strings.Join(t.Forms, ", "))
			b.WriteString("\n")
		}
		if len(t.SeeAlso) > 0 {
			b.WriteString("\nSee also: ")
			b.WriteString(strings.Join(t.SeeAlso, ", "))
			b.WriteString("\n")
		}
	}
	return b.String()
}

// RenderIntentsSkill renders the intent taxonomy skill's markdown: the
// analytic intents, then those that route to tooling, each group sorted
// by ID, with the intent's phrasings and alternative data shapes.
// Deterministic.
func RenderIntentsSkill() string {
	all := Intents()
	sort.SliceStable(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	var b strings.Builder
	skillFrontmatter(&b, skills.VirtualIntents, intentsSkillDescription)
	b.WriteString("# Intents\n\n")
	b.WriteString("The closed set of question kinds an analysis can answer. Operator purposes and examples cite these IDs; ")
	b.WriteString("a shape lists the roles a cohort's fields must fill for the question to be answerable.\n")
	for _, section := range []struct {
		title    string
		analytic bool
	}{
		{"Analytic intents", true},
		{"Tooling intents", false},
	} {
		b.WriteString("\n## ")
		b.WriteString(section.title)
		b.WriteString("\n")
		for _, in := range all {
			if in.Analytic != section.analytic {
				continue
			}
			renderIntent(&b, in)
		}
	}
	return b.String()
}

func renderIntent(b *strings.Builder, in descriptor.Intent) {
	b.WriteString("\n### ")
	b.WriteString(in.ID)
	b.WriteString("\n\n")
	b.WriteString(in.Label)
	b.WriteString(".\n")
	if len(in.Sounds) > 0 {
		b.WriteString("\nSounds like:\n")
		for _, s := range in.Sounds {
			b.WriteString("- ")
			b.WriteString(s)
			b.WriteString("\n")
		}
	}
	if len(in.Shapes) > 0 {
		b.WriteString("\nShapes:\n")
		for _, sh := range in.Shapes {
			parts := make([]string, len(sh.Roles))
			for i, r := range sh.Roles {
				kinds := make([]string, len(r.Kinds))
				for k, kind := range r.Kinds {
					kinds[k] = string(kind)
				}
				parts[i] = r.Name + " (" + strings.Join(kinds, "|") + ", " + roleCount(r) + ")"
			}
			b.WriteString("- ")
			b.WriteString(strings.Join(parts, " + "))
			b.WriteString("\n")
		}
	}
}

// roleCount renders how many fields a role takes.
func roleCount(r descriptor.Role) string {
	switch {
	case r.Max == descriptor.RoleUnbounded:
		return strconv.Itoa(r.Min) + " or more"
	case r.Min == 0 && r.Max == 1:
		return "optional"
	case r.Min == r.Max:
		return "exactly " + strconv.Itoa(r.Min)
	default:
		return strconv.Itoa(r.Min) + " to " + strconv.Itoa(r.Max)
	}
}
