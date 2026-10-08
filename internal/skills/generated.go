package skills

import (
	"regexp"
	"strings"
	"sync"
)

// Generated sections (U21). An atomic skill file may carry a marker LINE
//
//	<!-- generated: use-when -->
//	<!-- generated: reading-the-output -->
//
// that is replaced at READ time with a heading plus a body rendered from
// the documented operator's guidance metadata (its Purpose and
// Interpretation). The prose lives in the registries, never in the file:
// Raw serves the marker verbatim, every rendered read (Get, and the
// instance render in internal/descriptor) substitutes it. A marker is
// recognised only alone on its line (surrounding whitespace allowed);
// a marker whose section renders nothing — no metadata for the operator,
// or no renderer registered — is removed silently, together with one
// blank line it would otherwise leave doubled. A marker naming an
// unknown section is left untouched.
//
// The renderer lives in internal/descriptor (which imports this
// package), so it is registered at that package's init time
// (RegisterSectionRenderer), the same seam the virtual skills use.

// The generated-section names a marker may carry.
const (
	// SectionUseWhen renders `## Use when` from the operator's Purpose.
	SectionUseWhen = "use-when"
	// SectionReadingTheOutput renders `## Reading the output` from the
	// operator's Interpretation entries.
	SectionReadingTheOutput = "reading-the-output"
)

// GeneratedSections returns the known generated-section names, in the
// order they appear in an atomic skill.
func GeneratedSections() []string {
	return []string{SectionUseWhen, SectionReadingTheOutput}
}

// GeneratedMarker returns the marker line for section.
func GeneratedMarker(section string) string {
	return "<!-- generated: " + section + " -->"
}

var generatedMarker = regexp.MustCompile(`^<!--\s*generated:\s*([a-z0-9-]+)\s*-->$`)

// MarkerSection reports the section a line's generated marker names; ok
// is false for a line that is not a lone marker of a known section.
func MarkerSection(line string) (section string, ok bool) {
	m := generatedMarker.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return "", false
	}
	for _, s := range GeneratedSections() {
		if s == m[1] {
			return s, true
		}
	}
	return "", false
}

// SectionRenderer renders one generated section (heading included, no
// trailing newline) for an operator — the frontmatter `operator:` of
// the skill being served. It returns "" when the operator declares no
// metadata for the section.
type SectionRenderer func(operator, section string) string

var (
	sectionMu       sync.RWMutex
	sectionRenderer SectionRenderer
)

// RegisterSectionRenderer installs the full-instance section renderer
// Get uses. It panics on a nil renderer or a second registration — each
// a programming error that must fail loudly at init.
func RegisterSectionRenderer(r SectionRenderer) {
	if r == nil {
		panic("skills: nil section renderer")
	}
	sectionMu.Lock()
	defer sectionMu.Unlock()
	if sectionRenderer != nil {
		panic("skills: section renderer registered twice")
	}
	sectionRenderer = r
}

func registeredSectionRenderer() SectionRenderer {
	sectionMu.RLock()
	defer sectionMu.RUnlock()
	return sectionRenderer
}

// RenderGenerated replaces every generated-section marker line of body
// with render(section). A "" result removes the marker line, and a blank
// line that removal would leave doubled. A body with no marker is
// returned unchanged.
func RenderGenerated(body string, render func(section string) string) string {
	if !strings.Contains(body, "<!--") {
		return body
	}
	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines))
	changed := false
	for i := 0; i < len(lines); i++ {
		sec, ok := MarkerSection(lines[i])
		if !ok {
			out = append(out, lines[i])
			continue
		}
		changed = true
		r := ""
		if render != nil {
			r = strings.TrimRight(render(sec), "\n")
		}
		if r != "" {
			out = append(out, r)
			continue
		}
		prevBlank := len(out) == 0 || strings.TrimSpace(out[len(out)-1]) == ""
		if prevBlank && i+1 < len(lines) && strings.TrimSpace(lines[i+1]) == "" {
			i++
		}
	}
	if !changed {
		return body
	}
	return strings.Join(out, "\n")
}

// RenderFull is the full-instance render Get serves for a raw embedded
// body: every feature fence kept and every fence marker stripped
// (RenderFences with keep == nil), then every generated-section marker
// substituted by the registered renderer for the body's frontmatter
// `operator:` (removed when none is registered or the body names no
// operator). A body whose fences do not parse is returned raw.
func RenderFull(raw string) string {
	out, err := RenderFences(raw, nil)
	if err != nil {
		return raw
	}
	r := registeredSectionRenderer()
	op := ""
	if md, ok := parseMetadata(out); ok {
		op = md.Operator
	}
	return RenderGenerated(out, func(section string) string {
		if r == nil || op == "" {
			return ""
		}
		return r(op, section)
	})
}
