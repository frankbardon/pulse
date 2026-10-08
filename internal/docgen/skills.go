package docgen

import (
	"regexp"
	"strings"

	"github.com/frankbardon/pulse/internal/skills"
)

// examplesSearchTool names the lines a skill page leaves unlinked: a
// `pulse_examples_search tags=[…]` reference is a tool call, not a
// skill name.
const examplesSearchTool = "pulse_examples_search"

// backtickSpan matches one single-backtick inline code span.
var backtickSpan = regexp.MustCompile("`([^`\n]+)`")

// renderSkill renders skills/<stem>.md: the body exactly as the
// instance serves it (Discovery.Skill — fences rendered against the
// pruned graph, generated sections substituted), with two book-only
// changes that leave the served text itself untouched:
//
//   - the YAML frontmatter is wrapped in a ```yaml code block, so
//     Markdown renders it as data rather than a rule and a heading;
//   - outside code blocks, a backticked span naming another exported
//     skill becomes a relative link to its page. A line naming
//     pulse_examples_search stays plain.
func (g *gen) renderSkill(stem string) string {
	body, ok := g.disc.Skill(stem)
	if !ok {
		return ""
	}
	return linkSkillStems(fenceFrontmatter(body), stem, g.skillSet)
}

// fenceFrontmatter wraps body's leading `---` frontmatter block in a
// ```yaml code block; a body without one is returned unchanged.
func fenceFrontmatter(body string) string {
	if !strings.HasPrefix(body, "---\n") {
		return body
	}
	end := strings.Index(body[4:], "\n---\n")
	if end < 0 {
		return body
	}
	fm := body[4 : 4+end]
	rest := body[4+end+len("\n---\n"):]
	return "```yaml\n" + fm + "\n```\n" + rest
}

// linkSkillStems links, in every line outside a code block, each
// backticked span naming a skill in set (other than self) to its
// sibling page. Lines naming pulse_examples_search, and spans already
// the text of a link, stay as they are.
func linkSkillStems(body, self string, set map[string]bool) string {
	lines := strings.Split(body, "\n")
	inCode := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inCode = !inCode
			continue
		}
		if inCode || !strings.Contains(line, "`") || strings.Contains(line, examplesSearchTool) {
			continue
		}
		lines[i] = linkLine(line, self, set)
	}
	return strings.Join(lines, "\n")
}

func linkLine(line, self string, set map[string]bool) string {
	var b strings.Builder
	last := 0
	for _, m := range backtickSpan.FindAllStringSubmatchIndex(line, -1) {
		start, end := m[0], m[1]
		stem := line[m[2]:m[3]]
		if stem == self || !set[stem] {
			continue
		}
		if start > 0 && line[start-1] == '[' && strings.HasPrefix(line[end:], "](") {
			continue
		}
		b.WriteString(line[last:start])
		b.WriteString("[`")
		b.WriteString(stem)
		b.WriteString("`](")
		b.WriteString(stem)
		b.WriteString(".md)")
		last = end
	}
	if last == 0 {
		return line
	}
	b.WriteString(line[last:])
	return b.String()
}

// renderSkillIndex renders skills.md: every exported skill with its
// listed (instance-scrubbed) description.
func renderSkillIndex(list []skills.Metadata) string {
	var b strings.Builder
	b.WriteString("# Skills\n\n")
	b.WriteString("Every skill this instance serves, as an agent reads it, sorted by name.\n\n")
	for _, md := range list {
		b.WriteString("- [`")
		b.WriteString(md.Name)
		b.WriteString("`](")
		b.WriteString(skillPath(md.Name))
		b.WriteString(")")
		if d := strings.TrimSpace(strings.ReplaceAll(md.Description, "\n", " ")); d != "" {
			b.WriteString(": ")
			b.WriteString(d)
		}
		b.WriteString("\n")
	}
	return b.String()
}
