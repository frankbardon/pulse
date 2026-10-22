package docgen

import (
	"embed"
	"io/fs"
	"path"
	"strings"
)

// Hand-written prose files. Every page part a person writes rather than
// a registry renders lives in a file under guides/, embedded so an
// export outside the repository carries it:
//
//	guides/index.md        the "What can Pulse answer?" landing page
//	guides/<intent_id>.md  one question guide per analytic intent
//
// A file is a sequence of named sections, each opened by a whole line
// "<!-- section <name> -->" and running to the next such line or the
// end of the file. Text before the first marker is not allowed (the
// prose tests refuse it), and each page reads only the section names
// it knows (guideSections, landingSections). Render copies a section
// into the page between preserve markers, so regeneration reproduces
// it byte for byte; a missing file or section renders the page's
// generated fallback instead, outside any preserve span.
//
//go:embed guides/*.md
var embeddedProse embed.FS

// proseFS is the prose source Render reads. A package variable so the
// package's own tests can render against a synthetic prose tree.
var proseFS fs.FS = embeddedProse

const (
	proseDir           = "guides"
	proseSectionPrefix = "<!-- section "
	proseSectionSuffix = " -->"

	landingProseFile = "index"
)

// Section names a prose file may carry.
const (
	sectionIntro    = "intro"    // landing page: the hand-written introduction
	sectionQuestion = "question" // guide part 1: the question and sample phrasings
	sectionPitfalls = "pitfalls" // guide part 5: the pitfalls
)

// landingSections and guideSections are the section names each page
// reads, in page order.
var (
	landingSections = []string{sectionIntro}
	guideSections   = []string{sectionQuestion, sectionPitfalls}
)

// proseFile is one parsed prose file: its sections by name, and the
// section names in file order. bad lists every structural problem (a
// duplicate section, text before the first marker); the page still
// renders what parsed, and the prose tests fail on any problem.
type proseFile struct {
	sections map[string]string
	order    []string
	bad      []string
}

// readProse reads and parses guides/<stem>.md from proseFS; false when
// the file does not exist.
func readProse(stem string) (proseFile, bool) {
	data, err := fs.ReadFile(proseFS, path.Join(proseDir, stem+".md"))
	if err != nil {
		return proseFile{}, false
	}
	return parseProse(string(data)), true
}

// parseProse splits a prose file into its sections. A section's body
// has its leading and trailing blank lines dropped and ends with
// exactly one newline; a section with no text is absent.
func parseProse(src string) proseFile {
	pf := proseFile{sections: map[string]string{}}
	name := ""
	var body []string
	flush := func() {
		text := strings.Trim(strings.Join(body, "\n"), "\n")
		switch {
		case name == "":
			if strings.TrimSpace(text) != "" {
				pf.bad = append(pf.bad, "text before the first section marker")
			}
		case strings.TrimSpace(text) != "":
			pf.sections[name] = text + "\n"
		}
		body = nil
	}
	for _, line := range strings.Split(src, "\n") {
		trim := strings.TrimRight(line, "\r")
		if strings.HasPrefix(trim, proseSectionPrefix) && strings.HasSuffix(trim, proseSectionSuffix) {
			flush()
			name = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trim, proseSectionPrefix), proseSectionSuffix))
			for _, seen := range pf.order {
				if seen == name {
					pf.bad = append(pf.bad, "duplicate section "+name)
				}
			}
			pf.order = append(pf.order, name)
			continue
		}
		body = append(body, trim)
	}
	flush()
	return pf
}

// section returns the named section's text scrubbed for the instance
// (unchanged on an instance hiding nothing it names); "" when the file
// lacks the section or the scrub empties it.
func (g *gen) section(pf proseFile, name string) string {
	text, ok := pf.sections[name]
	if !ok {
		return ""
	}
	text = strings.Trim(g.scrub.Text(text), "\n")
	if strings.TrimSpace(text) == "" {
		return ""
	}
	return text + "\n"
}

// writePreserved writes body between the begin and end preserve markers
// of span id. body must end with a newline.
func writePreserved(b *strings.Builder, id, body string) {
	b.WriteString(preserveBeginPrefix + id + preserveSuffix + "\n")
	b.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		b.WriteString("\n")
	}
	b.WriteString(preserveEndPrefix + id + preserveSuffix + "\n")
}
