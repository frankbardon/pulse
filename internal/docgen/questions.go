package docgen

import (
	"bytes"
	"encoding/json"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/internal/examples"
)

// Question pages: the "What can Pulse answer?" landing page and one
// guide per analytic intent, titled by Intent.Label, slugged by the
// intent ID.
const (
	questionsPage  = "questions.md"
	questionsTitle = "What can Pulse answer?"

	// relatedIntentLimit caps a guide's related-question links.
	relatedIntentLimit = 3
)

func questionPath(intent string) string { return path.Join("questions", intent+".md") }

// questionGuide is one rendered question guide: its intent and the
// operators serving it the instance offers, sorted.
type questionGuide struct {
	intent descriptor.Intent
	ops    []string
}

// questionGuides returns the instance's question guides in taxonomy
// order: one per analytic intent the instance keeps that some visible,
// catalogued operator serves. Call after categories (it reads
// opCategory).
func (g *gen) questionGuides() []questionGuide {
	var out []questionGuide
	for _, in := range descx.Intents() {
		if !in.Analytic || !g.intentVisible(in.ID) {
			continue
		}
		var ops []string
		for _, op := range g.graph.OperatorsServing(in.ID) {
			if _, ok := g.opCategory[op]; ok {
				ops = append(ops, op)
			}
		}
		if len(ops) == 0 {
			continue
		}
		out = append(out, questionGuide{intent: in, ops: ops})
	}
	return out
}

func (g *gen) intentVisible(id string) bool {
	return g.graph.Has(descx.OntologyID(descriptor.OntologyNodeIntent, id))
}

// firstSound returns the intent's first sample phrasing the scrub
// leaves; "" when none.
func (g *gen) firstSound(in descriptor.Intent) string {
	for _, s := range in.Sounds {
		if s = g.text(s); s != "" {
			return s
		}
	}
	return ""
}

// renderQuestions renders questions.md: the preserved intro, one row
// per question guide, then the utility intents, which route to tooling
// and have no guide.
func (g *gen) renderQuestions(guides []questionGuide) string {
	var b strings.Builder
	b.WriteString("# " + questionsTitle + "\n\n")
	pf, _ := readProse(landingProseFile)
	if intro := g.section(pf, sectionIntro); intro != "" {
		writePreserved(&b, landingSpanID(sectionIntro), intro)
	} else {
		b.WriteString("Each guide below starts from one kind of question and walks from the data you have to the analysis that answers it.\n")
	}
	b.WriteString("\n| Question kind | Sounds like |\n|---|---|\n")
	for _, qg := range guides {
		b.WriteString("| [")
		b.WriteString(cell(qg.intent.Label))
		b.WriteString("](")
		b.WriteString(questionPath(qg.intent.ID))
		b.WriteString(") | ")
		b.WriteString(cell(g.firstSound(qg.intent)))
		b.WriteString(" |\n")
	}
	var utility []string
	for _, in := range descx.Intents() {
		if in.Analytic || !g.intentVisible(in.ID) {
			continue
		}
		line := "**" + in.Label + "**"
		if s := g.firstSound(in); s != "" {
			line += " — " + s
		}
		utility = append(utility, line)
	}
	if len(utility) > 0 {
		b.WriteString("\nSome jobs are not analysis questions, so they have no guide here; Pulse routes them to its data tools instead:\n\n")
		for _, u := range utility {
			b.WriteString("- ")
			b.WriteString(u)
			b.WriteString("\n")
		}
	}
	return b.String()
}

func landingSpanID(section string) string { return "questions-" + section }

func guideSpanID(intent, section string) string { return "guide-" + intent + "-" + section }

// renderGuide renders one question guide in six parts: the question,
// picking an analysis, a worked story, reading the result, pitfalls and
// next steps.
func (g *gen) renderGuide(qg questionGuide, guides []questionGuide, readingHome, homes map[string]string) string {
	in := qg.intent
	page := questionPath(in.ID)
	pf, _ := readProse(in.ID)
	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(in.Label)
	b.WriteString("\n\n## The question\n\n")
	if q := g.section(pf, sectionQuestion); q != "" {
		writePreserved(&b, guideSpanID(in.ID, sectionQuestion), q)
	} else {
		b.WriteString("Questions of this kind sound like:\n\n")
		for _, s := range in.Sounds {
			if s = g.text(s); s != "" {
				b.WriteString("- ")
				b.WriteString(s)
				b.WriteString("\n")
			}
		}
	}

	stories := g.guideStories(in.ID)
	ok := len(stories) > 0
	storyOps := storyOperators(stories)
	g.writePickAnalysis(&b, qg, storyOps)

	b.WriteString("\n## A worked example\n\n")
	if ok {
		g.writeGetTheData(&b, stories[0])
		for i, story := range stories {
			if i > 0 {
				b.WriteString("\nAnother worked example:\n\n")
			}
			g.writeStory(&b, story)
		}
	} else {
		b.WriteString("No worked example is published for this question yet.")
		if g.inst.Enabled(descx.FeatureName(descx.FeatureKindCapability, "recommend")) {
			b.WriteString(" `pulse recommend --intent " + in.ID + "` drafts requests for it.")
		}
		b.WriteString("\n")
	}

	b.WriteString("\n## How to read the result\n")
	g.writeHowToRead(&b, storyOps, page, readingHome, homes)

	b.WriteString("\n## Pitfalls\n\n")
	if p := g.section(pf, sectionPitfalls); p != "" {
		writePreserved(&b, guideSpanID(in.ID, sectionPitfalls), p)
	} else {
		b.WriteString("Each operator's catalog entry lists its assumptions and when to reach for something else; read them before trusting a figure.\n")
	}

	g.writeNextSteps(&b, qg, guides, storyOps)
	return b.String()
}

// shapeLine renders one data shape an intent applies to, its roles
// joined: "`outcome`: one numeric, categorical or bool field".
func shapeLine(s descriptor.Shape) string {
	parts := make([]string, 0, len(s.Roles))
	for _, r := range s.Roles {
		kinds := make([]string, 0, len(r.Kinds))
		for _, k := range r.Kinds {
			kinds = append(kinds, string(k))
		}
		parts = append(parts, "`"+r.Name+"`: "+roleCount(r)+" "+orList(kinds)+" "+plural(r, "field", "fields"))
	}
	return strings.Join(parts, "; ")
}

func roleCount(r descriptor.Role) string {
	switch {
	case r.Min == 0 && r.Max == 1:
		return "optionally one"
	case r.Min == r.Max && r.Min == 1:
		return "one"
	case r.Min == r.Max:
		return strconv.Itoa(r.Min)
	case r.Max == descriptor.RoleUnbounded:
		return strconv.Itoa(r.Min) + " or more"
	default:
		return strconv.Itoa(r.Min) + " to " + strconv.Itoa(r.Max)
	}
}

func plural(r descriptor.Role, one, many string) string {
	if r.Max == 1 {
		return one
	}
	return many
}

func orList(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " or " + items[len(items)-1]
}

// writePickAnalysis renders part 2: the data shapes the question fits,
// then every operator serving the intent, grouped by catalog category,
// each with its plain-words Purpose and Level.
//
// storyOps are the worked examples' operators: the line above the
// tables names them, so a reader knows where the example starts even
// when it is not a basic one.
func (g *gen) writePickAnalysis(b *strings.Builder, qg questionGuide, storyOps []string) {
	b.WriteString("\n## Pick your analysis\n\n")
	b.WriteString("This question fits data shaped like:\n\n")
	for _, s := range qg.intent.Shapes {
		b.WriteString("- ")
		b.WriteString(shapeLine(s))
		b.WriteString("\n")
	}
	b.WriteString("\nThe operators that answer it, by kind; each name links to its catalog entry. Basic ones need the least setup")
	if len(storyOps) > 0 {
		quoted := make([]string, len(storyOps))
		for i, op := range storyOps {
			quoted[i] = "`" + op + "`"
		}
		b.WriteString("; the worked example below uses ")
		b.WriteString(andList(quoted))
	}
	b.WriteString(".\n")
	serving := map[string]bool{}
	for _, op := range qg.ops {
		serving[op] = true
	}
	for _, c := range g.cats {
		var rows []entry
		for _, e := range c.ops {
			if serving[e.name] {
				rows = append(rows, e)
			}
		}
		if len(rows) == 0 {
			continue
		}
		sort.SliceStable(rows, func(i, j int) bool {
			return levelOrder(rows[i]) < levelOrder(rows[j])
		})
		b.WriteString("\n### ")
		b.WriteString(c.title)
		b.WriteString("\n\n| Use | In plain words | Level |\n|---|---|---|\n")
		for _, e := range rows {
			level := ""
			if e.purpose != nil {
				level = string(e.purpose.Level)
			}
			b.WriteString("| [`")
			b.WriteString(e.name)
			b.WriteString("`](../")
			b.WriteString(catalogPath(c.key))
			b.WriteString("#")
			b.WriteString(operatorAnchor(e.name))
			b.WriteString(") | ")
			b.WriteString(cell(g.plain(e)))
			b.WriteString(" | ")
			b.WriteString(cell(level))
			b.WriteString(" |\n")
		}
	}
}

// levelOrder ranks basic → intermediate → advanced → ungraded.
func levelOrder(e entry) int {
	if e.purpose == nil {
		return 3
	}
	switch e.purpose.Level {
	case descriptor.LevelBasic:
		return 0
	case descriptor.LevelIntermediate:
		return 1
	case descriptor.LevelAdvanced:
		return 2
	}
	return 3
}

// guideStoryOf is a story shown on a guide: the example and its golden.
type guideStoryOf struct {
	ex     *examples.Example
	golden []byte
}

// guideStories returns the worked stories of intent, in example-name
// order: every story example whose FIRST _meta.intents entry is
// intent, which the instance shows (no operator hidden) and which has
// a committed golden. Every one renders on the guide, so a story is
// never silently dropped because an earlier one claims its intent.
func (g *gen) guideStories(intent string) []guideStoryOf {
	var out []guideStoryOf
	for _, name := range examples.Stories() {
		if !g.disc.ExampleVisible(name) {
			continue
		}
		ex, ok := g.disc.Example(name)
		if !ok || len(ex.Intents) == 0 || ex.Intents[0] != intent {
			continue
		}
		hidden := false
		for _, op := range ex.Operators {
			if !g.operatorVisible(op) {
				hidden = true
			}
		}
		if hidden {
			continue
		}
		golden, ok := examples.StoryGolden(name)
		if !ok {
			continue
		}
		out = append(out, guideStoryOf{ex: ex, golden: golden})
	}
	return out
}

// storyOperators returns the operators of stories, deduplicated in
// first-seen order: the operators the guide's reading and skill links
// cover.
func storyOperators(stories []guideStoryOf) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range stories {
		for _, op := range s.ex.Operators {
			if !seen[op] {
				seen[op] = true
				out = append(out, op)
			}
		}
	}
	return out
}

// exampleDataURL is the book section on writing the example cohorts. An
// absolute URL, as conceptsBase, so an exported guide still links to a
// page that exists.
const exampleDataURL = "https://frankbardon.github.io/pulse/examples/library.html#get-the-data"

// writeGetTheData renders the steps that make a worked example runnable
// as shown: write the example fixture cohorts, work from their directory
// (CLI cohort paths resolve against the working directory), save the
// request and run it. The examples surface is core; without the process
// capability there is nothing to run, so it writes nothing.
func (g *gen) writeGetTheData(b *strings.Builder, s guideStoryOf) {
	if !g.inst.Enabled(descx.FeatureName(descx.FeatureKindCapability, "process")) {
		return
	}
	b.WriteString("To run the example below yourself, write the example cohorts once and work from their directory, since command-line cohort paths resolve against the working directory ([Get the data](")
	b.WriteString(exampleDataURL)
	b.WriteString(")). ")
	b.WriteString(scratchDirLine)
	b.WriteString("\n\n```bash\npulse examples data --out pulse-data\ncd pulse-data\npulse examples show ")
	b.WriteString(s.ex.Name)
	b.WriteString(" > req.json\npulse api process --request req.json --json\n```\n\n")
}

// scratchDirLine tells the reader where to run the get-the-data steps:
// written inside PULSE_DATA_DIR, the cohorts would nest in the
// directory `pulse mcp` serves.
const scratchDirLine = "Start in a scratch directory outside your `PULSE_DATA_DIR`, so `pulse-data` does not nest inside the directory `pulse mcp` serves:"

// reformatNote follows a result block that prints a float in exponent
// notation. The block is the wire's own output (return.precision writes
// strconv 'g', as `pulse api process --json` prints it); a JSON tool
// such as jq reprints the same number in its own notation, which a
// reader comparing the two would otherwise take for a difference.
const reformatNote = "Exponents print here as `pulse api process --json` prints them (`6.069e+04`); a tool that reformats JSON, such as `jq`, may print the same number as `6.069E+4`.\n\n"

// hasExponent reports whether a result block holds a JSON number in
// exponent notation.
func hasExponent(block []byte) bool {
	for i := 1; i+1 < len(block); i++ {
		if (block[i] == 'e' || block[i] == 'E') && block[i-1] >= '0' && block[i-1] <= '9' && (block[i+1] == '+' || block[i+1] == '-') {
			return true
		}
	}
	return false
}

// andList joins items as "a", "a and b" or "a, b and c".
func andList(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// writeStory renders part 3: the story's question, its request, the
// committed golden response verbatim and its interpretation.
func (g *gen) writeStory(b *strings.Builder, s guideStoryOf) {
	b.WriteString("**")
	b.WriteString(g.text(s.ex.Question))
	b.WriteString("**\n\nThe request (example `")
	b.WriteString(s.ex.Name)
	b.WriteString("`; `pulse examples show ")
	b.WriteString(s.ex.Name)
	b.WriteString("` prints it):\n\n```json")
	b.WriteString(storyCohortTag(s.ex.Body))
	b.WriteString("\n")
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, s.ex.Body, "", "  "); err != nil {
		pretty.Reset()
		pretty.Write(s.ex.Body)
	}
	b.Write(pretty.Bytes())
	b.WriteString("\n```\n\nThe result:\n\n```json\n")
	b.WriteString(strings.TrimRight(string(s.golden), "\n"))
	b.WriteString("\n```\n\n")
	if hasExponent(s.golden) {
		b.WriteString(reformatNote)
	}
	b.WriteString(g.text(s.ex.Interpretation))
	b.WriteString("\n")
}

// writeHowToRead renders part 4: the Interpretation entries of the
// stories' operators (ops), each field's meaning, bands, sign and
// caveats, and a pointer to the operator's reading page. Without a
// story it points at the reading pages.
func (g *gen) writeHowToRead(b *strings.Builder, ops []string, page string, readingHome, homes map[string]string) {
	wrote := false
	for _, op := range ops {
		var fields []descriptor.Interpretation
		for _, in := range g.interpretationsOf(op) {
			if g.fieldVisible(in) && g.fieldHasContent(in) {
				fields = append(fields, in)
			}
		}
		if len(fields) == 0 {
			continue
		}
		wrote = true
		b.WriteString("\n### `")
		b.WriteString(op)
		b.WriteString("`\n")
		for _, in := range fields {
			b.WriteString("\n**`")
			b.WriteString(in.Field)
			b.WriteString("`**\n")
			b.WriteString(g.renderField(in, "", nil))
			if home, ok := homes[in.Shared]; ok && in.Shared != "" {
				b.WriteString("\nFollows the shared reading: see [")
				b.WriteString(sharedTitle(in.Shared))
				b.WriteString("](")
				b.WriteString(relPath(page, home))
				b.WriteString("#")
				b.WriteString(sharedAnchor(in.Shared))
				b.WriteString(").\n")
			}
		}
		if rp, ok := readingHome[op]; ok {
			b.WriteString("\nMore on reading it: [")
			b.WriteString(op)
			b.WriteString("](")
			b.WriteString(relPath(page, rp))
			b.WriteString("#")
			b.WriteString(operatorAnchor(op))
			b.WriteString(").\n")
		}
	}
	if !wrote {
		b.WriteString("\nThe figures these operators return read as their names say; for the ones that need more, see [Reading your results](")
		b.WriteString(relPath(page, "reading.md"))
		b.WriteString(").\n")
	}
}

// relPath links target (export-root relative) from page.
func relPath(page, target string) string {
	depth := strings.Count(page, "/")
	return strings.Repeat("../", depth) + target
}

// writeNextSteps renders part 6: related questions (the guides sharing
// the most serving operators), the skills to read and the CLI leaves
// that act on the question.
func (g *gen) writeNextSteps(b *strings.Builder, qg questionGuide, guides []questionGuide, storyOps []string) {
	b.WriteString("\n## Next steps\n")
	own := map[string]bool{}
	for _, op := range qg.ops {
		own[op] = true
	}
	type rel struct {
		qg    questionGuide
		n     int
		order int
	}
	var rels []rel
	for i, other := range guides {
		if other.intent.ID == qg.intent.ID {
			continue
		}
		n := 0
		for _, op := range other.ops {
			if own[op] {
				n++
			}
		}
		if n > 0 {
			rels = append(rels, rel{qg: other, n: n, order: i})
		}
	}
	sort.SliceStable(rels, func(i, j int) bool {
		if rels[i].n != rels[j].n {
			return rels[i].n > rels[j].n
		}
		return rels[i].order < rels[j].order
	})
	if len(rels) > relatedIntentLimit {
		rels = rels[:relatedIntentLimit]
	}
	if len(rels) > 0 {
		b.WriteString("\n**Related questions:**\n\n")
		for _, r := range rels {
			b.WriteString("- [")
			b.WriteString(r.qg.intent.Label)
			b.WriteString("](")
			b.WriteString(r.qg.intent.ID)
			b.WriteString(".md)\n")
		}
	}

	var skillLinks []string
	seen := map[string]bool{}
	addSkill := func(stem string) {
		if stem == "" || seen[stem] || !g.skillSet[stem] {
			return
		}
		seen[stem] = true
		skillLinks = append(skillLinks, "[`"+stem+"`](../"+skillPath(stem)+")")
	}
	if list, err := g.inst.SkillsForIntent(qg.intent.ID); err == nil {
		for _, md := range list {
			if md.Operator == "" {
				addSkill(md.Name)
			}
		}
	}
	for _, op := range storyOps {
		addSkill(g.operatorSkill(op))
	}
	if len(skillLinks) > 0 {
		b.WriteString("\n**Skills:** ")
		b.WriteString(strings.Join(skillLinks, ", "))
		b.WriteString("\n")
	}

	var cli []string
	if g.inst.Enabled(descx.FeatureName(descx.FeatureKindCapability, "recommend")) {
		cli = append(cli, "`pulse recommend --intent "+qg.intent.ID+" --cohort PATH` drafts requests for this question bound to your cohort's fields.")
	}
	cli = append(cli, "`pulse api predict` checks a request against your cohort without running it.")
	if g.inst.Enabled(descx.FeatureName(descx.FeatureKindCapability, "process")) {
		cli = append(cli, "`pulse api process` runs it.")
	}
	if g.inst.Enabled(descx.FeatureName(descx.FeatureKindCapability, "explain")) {
		cli = append(cli, "`pulse explain --response FILE --request REQ` reads a result back in plain words: save the `pulse api process --json` output as FILE, and pass the request that produced it as REQ so the findings name its fields.")
	}
	writeList(b, "From the command line", cli)
}

// storyCohortTag is the ` cohort=<name>[,<name>…]` info-string tag of a
// story request's fence: the example fixture cohorts its cohort
// references read (the filename stems, in a deterministic walk order), so
// TestDocsSnippetsRun runs the rendered request on the fixtures every
// example runs on. A body naming no cohort gets no tag.
func storyCohortTag(body []byte) string {
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return ""
	}
	var stems []string
	seen := map[string]bool{}
	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if ref, ok := t[k].(map[string]any); ok && k == "cohort" {
					name, _ := ref["filename"].(string)
					if i := strings.Index(name, "#"); i >= 0 {
						name = name[:i]
					}
					name = strings.TrimSuffix(name, ".pulse")
					if name != "" && !seen[name] {
						seen[name] = true
						stems = append(stems, name)
					}
				}
				walk(t[k])
			}
		case []any:
			for _, c := range t {
				walk(c)
			}
		}
	}
	walk(root)
	if len(stems) == 0 {
		return ""
	}
	return " cohort=" + strings.Join(stems, ",")
}
