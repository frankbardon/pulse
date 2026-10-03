package descriptor

import (
	stderrors "errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/skills"
)

// Embedder skills (U10, E5-S1) — pulse.Options.Extensions.Skills.
//
// An embedder ships an fs.FS of top-level `.md` files in two shapes:
//
//   - atomic `op-<category>-<kebab>.md` (`kind: operator`) documenting
//     ONE registered extension operator: stem = OperatorStem(operator),
//     `category:` = the operator's prefix (AGG, ATTR, FILTER, GROUP, WIN,
//     FEAT, TEST, SYNTH), the family's required `##` sections;
//   - topical `ext-<kebab>.md` (`kind: design`), optionally `requires:`.
//
// LoadExtensionSkills validates them HARD at pulse.New, against the
// FULL extended graph (every registration, before a feature profile
// hides any), so validity never depends on the profile. The checks, in
// order, each failing with PULSE_EXTENSION_SKILL_INVALID (reason in
// details) unless noted:
//
//  1. layout — the root must read; every entry a regular `.md` file;
//  2. collision — a stem taken by a built-in skill (embedded or
//     virtual) or shipped twice → PULSE_EXTENSION_SKILL_COLLISION;
//  3. stem — `op-<category>-<kebab>` or `ext-<kebab>`;
//  4. frontmatter / name / description — parsed as the embedded pack
//     is, `name` = stem, description non-empty;
//  5. kind / operator / category — atomic documents a registered
//     extension operator its stem and category agree with; topical is
//     `kind: design` with no `operator:` / `category:`; only topical
//     skills may carry `requires:`, each entry a feature or a
//     registered extension operator (requires);
//  6. sections — the family's required `##` headings
//     (skills.RequiredSections);
//  7. budget — skills.BodyBudget, HARD (one byte over fails);
//  8. fence — skills.ParseFences + every name a node of the graph;
//  9. fence_coverage — every feature name (built-in operator,
//     `<kind>:<name>` feature, feature-owned `pulse_*` tool, registered
//     extension operator) sits in a fence naming it, unless the skill is
//     pruned with it (its own operator + that operator's DependsOn,
//     transitively; capability:synth for a synth distribution; a
//     topical skill's requires); the description holds none unguarded;
//  10. see — every backticked kebab span in `## See` is a skill the
//     extended graph carries (built-in, virtual or embedder).
//
// The instance graph (extendOntology) then adds each skill whose
// subject survives the profile: an atomic skill whose operator is
// hidden — dropped from the snapshot before the graph is built — is no
// node, a topical one whose requires names a hidden extension operator
// neither, and a requires on a hidden built-in prunes it like any
// topical skill. Discovery serves the survivors exactly like built-ins.

// ExtensionSkill is one validated embedder skill: its parsed frontmatter
// and its file verbatim (fences and all).
type ExtensionSkill struct {
	Metadata skills.Metadata
	Raw      string
}

// Reasons carried in PULSE_EXTENSION_SKILL_* details["reason"].
const (
	SkillReasonLayout        = "layout"
	SkillReasonBuiltin       = "builtin"
	SkillReasonDuplicate     = "duplicate"
	SkillReasonStem          = "stem"
	SkillReasonFrontmatter   = "frontmatter"
	SkillReasonName          = "name"
	SkillReasonDescription   = "description"
	SkillReasonKind          = "kind"
	SkillReasonOperator      = "operator"
	SkillReasonCategory      = "category"
	SkillReasonRequires      = "requires"
	SkillReasonSections      = "sections"
	SkillReasonBudget        = "budget"
	SkillReasonFence         = "fence"
	SkillReasonFenceCoverage = "fence_coverage"
	SkillReasonSee           = "see"
)

var (
	extAtomicStem  = regexp.MustCompile(`^op-[a-z]+(-[a-z0-9]+)+$`)
	extTopicalStem = regexp.MustCompile(`^ext-[a-z0-9]+(-[a-z0-9]+)*$`)
	// seeStemSpan is a backticked `## See` span shaped like a skill stem.
	seeStemSpan = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)
)

func skillError(code errors.Code, skill, reason, msg string, details map[string]any) error {
	if details == nil {
		details = map[string]any{}
	}
	details["skill"] = skill
	details["reason"] = reason
	return errors.NewCodedErrorWithDetails(code, "extension skill "+skill+": "+msg, details)
}

func skillInvalid(skill, reason, msg string, details map[string]any) error {
	return skillError(errors.PULSE_EXTENSION_SKILL_INVALID, skill, reason, msg, details)
}

// LoadExtensionSkills reads and validates every skill in fsys against
// ext (the snapshot of EVERY registration — not the profile-filtered
// one) and deps (each extension operator's DependsOn). Nil fsys loads
// nothing. The skills come back sorted by name.
func LoadExtensionSkills(fsys fs.FS, ext *ExtensionsSnapshot, deps map[string][]string) ([]ExtensionSkill, error) {
	if fsys == nil {
		return nil, nil
	}
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, skillInvalid(".", SkillReasonLayout, "cannot read the skills fs.FS root: "+err.Error(), nil)
	}
	builtin := map[string]bool{}
	for _, md := range skills.List() {
		builtin[md.Name] = true
	}
	for _, v := range skills.ReservedVirtualNames() {
		builtin[v] = true
	}
	operators := extensionOperatorCategories(ext)

	var out []ExtensionSkill
	seen := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || path.Ext(name) != ".md" || !e.Type().IsRegular() {
			return nil, skillInvalid(name, SkillReasonLayout, "Extensions.Skills holds only top-level .md files", nil)
		}
		stem := strings.TrimSuffix(name, ".md")
		if builtin[stem] {
			return nil, skillError(errors.PULSE_EXTENSION_SKILL_COLLISION, stem, SkillReasonBuiltin, "the stem is a built-in skill; embedder skills never override or shadow the shipped pack", nil)
		}
		if seen[stem] {
			return nil, skillError(errors.PULSE_EXTENSION_SKILL_COLLISION, stem, SkillReasonDuplicate, "the stem is shipped more than once", nil)
		}
		seen[stem] = true
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, skillInvalid(stem, SkillReasonLayout, "cannot read "+name+": "+err.Error(), nil)
		}
		s, err := checkExtensionSkillHeader(stem, string(data), operators)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Metadata.Name < out[j].Metadata.Name })
	if len(out) == 0 {
		return nil, nil
	}

	// The full extended graph: base + every registration + every skill.
	full := &ExtensionsSnapshot{}
	if ext != nil {
		cp := *ext
		full = &cp
	}
	full.Skills = out
	g := extendOntology(BaseOntology(), full)
	for _, s := range out {
		if err := checkExtensionSkillRequires(s, g); err != nil {
			return nil, err
		}
	}
	list := fenceScanList()
	for op := range operators {
		list[op] = op
	}
	for _, s := range out {
		if err := checkExtensionSkillBody(s, g, list, deps); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// extensionOperatorCategories maps every registered extension operator
// to its category prefix (AGG_ACME_X → AGG).
func extensionOperatorCategories(ext *ExtensionsSnapshot) map[string]string {
	out := map[string]string{}
	if ext == nil {
		return out
	}
	for _, l := range extensionOperatorLists(ext) {
		for _, op := range l {
			cat, _, _ := strings.Cut(op.Name, "_")
			out[op.Name] = cat
		}
	}
	return out
}

// checkExtensionSkillHeader runs checks 3–5 that need no graph.
func checkExtensionSkillHeader(stem, raw string, operators map[string]string) (ExtensionSkill, error) {
	atomic := extAtomicStem.MatchString(stem)
	if !atomic && !extTopicalStem.MatchString(stem) {
		return ExtensionSkill{}, skillInvalid(stem, SkillReasonStem, "the stem must be op-<category>-<kebab> (atomic, one extension operator) or ext-<kebab> (topical)", nil)
	}
	md, ok := skills.ParseMetadata(raw)
	if !ok {
		return ExtensionSkill{}, skillInvalid(stem, SkillReasonFrontmatter, "no --- frontmatter block", nil)
	}
	if md.Name != stem {
		return ExtensionSkill{}, skillInvalid(stem, SkillReasonName, fmt.Sprintf("frontmatter name %q must equal the file stem", md.Name), map[string]any{"name": md.Name})
	}
	if strings.TrimSpace(md.Description) == "" {
		return ExtensionSkill{}, skillInvalid(stem, SkillReasonDescription, "frontmatter description is required", nil)
	}
	if atomic {
		if md.Kind != "operator" {
			return ExtensionSkill{}, skillInvalid(stem, SkillReasonKind, fmt.Sprintf("an op-* skill is kind: operator, not %q", md.Kind), map[string]any{"kind": md.Kind})
		}
		cat, ok := operators[md.Operator]
		if !ok {
			return ExtensionSkill{}, skillInvalid(stem, SkillReasonOperator, fmt.Sprintf("operator %q is not a registered extension operator", md.Operator), map[string]any{"operator": md.Operator})
		}
		if want := skills.OperatorStem(md.Operator); stem != want {
			return ExtensionSkill{}, skillInvalid(stem, SkillReasonStem, fmt.Sprintf("the skill documenting %s must be named %s", md.Operator, want), map[string]any{"operator": md.Operator, "want": want})
		}
		if md.Category != cat {
			return ExtensionSkill{}, skillInvalid(stem, SkillReasonCategory, fmt.Sprintf("category %q must be %q (the operator's prefix)", md.Category, cat), map[string]any{"category": md.Category, "want": cat})
		}
		if len(md.Requires) > 0 {
			return ExtensionSkill{}, skillInvalid(stem, SkillReasonRequires, "only topical (ext-*) skills carry requires: — an atomic skill follows its operator", nil)
		}
	} else if md.Kind != "design" || md.Operator != "" || md.Category != "" {
		return ExtensionSkill{}, skillInvalid(stem, SkillReasonKind, "an ext-* skill is kind: design with no operator: or category:", map[string]any{"kind": md.Kind})
	}
	return ExtensionSkill{Metadata: md, Raw: raw}, nil
}

// checkExtensionSkillRequires: each requires entry resolves to a
// feature or registered operator node of the extended graph.
func checkExtensionSkillRequires(s ExtensionSkill, g *OntologyGraph) error {
	for _, req := range s.Metadata.Requires {
		if !g.Has(FenceFeatureID(req)) {
			return skillInvalid(s.Metadata.Name, SkillReasonRequires, fmt.Sprintf("requires %q names no feature or registered operator (feature-profile spelling: operators bare, others <kind>:<name>)", req), map[string]any{"requires": req})
		}
	}
	return nil
}

// checkExtensionSkillBody runs checks 6–10 against the extended graph.
func checkExtensionSkillBody(s ExtensionSkill, g *OntologyGraph, list map[string]string, deps map[string][]string) error {
	md := s.Metadata
	if err := ValidateSkillFences(s.Raw, g); err != nil {
		var fe *skills.FenceError
		details := map[string]any{}
		if stderrors.As(err, &fe) {
			details["line"] = fe.Line
		}
		return skillInvalid(md.Name, SkillReasonFence, "feature fence: "+err.Error(), details)
	}
	full, err := skills.RenderFences(s.Raw, nil)
	if err != nil { // unreachable: ValidateSkillFences parsed it
		return skillInvalid(md.Name, SkillReasonFence, "feature fence: "+err.Error(), nil)
	}
	var missing []string
	for _, h := range skills.RequiredSections(md.Name, md.Category) {
		if !skills.HasHeading(full, h) {
			missing = append(missing, h)
		}
	}
	if len(missing) > 0 {
		return skillInvalid(md.Name, SkillReasonSections, "missing required section(s) "+strings.Join(missing, ", "), map[string]any{"missing": missing})
	}
	if budget, ok := skills.BodyBudget(md.Name, md.Kind); ok {
		if n := len(skills.StripFrontmatter(full)); n > budget {
			return skillInvalid(md.Name, SkillReasonBudget, fmt.Sprintf("body is %d bytes, over the %d-byte budget", n, budget), map[string]any{"bytes": n, "budget": budget})
		}
	}
	body, desc, err := fenceViolations(g, s.Raw, md.Description, list, extensionSkillGuards(g, md, deps))
	if err != nil { // unreachable: validated above
		return skillInvalid(md.Name, SkillReasonFence, "feature fence: "+err.Error(), nil)
	}
	if len(body)+len(desc) > 0 {
		tokens := slices.Sorted(maps.Keys(body))
		descTokens := slices.Sorted(maps.Keys(desc))
		return skillInvalid(md.Name, SkillReasonFenceCoverage,
			fmt.Sprintf("unfenced feature name(s) in the body %v, in the description %v — fence each (<!-- feature: NAME -->) or route to it without naming it", tokens, descTokens),
			map[string]any{"body": tokens, "description": descTokens})
	}
	for _, span := range backtickSpans(seeSection(full)) {
		if _, isTags := seeTags(span); isTags || !seeStemSpan.MatchString(span) {
			continue
		}
		if !g.Has(OntologyID(descriptor.OntologyNodeSkill, span)) {
			return skillInvalid(md.Name, SkillReasonSee, fmt.Sprintf("## See names %q, which is no skill", span), map[string]any{"see": span})
		}
	}
	return nil
}

// extensionSkillGuards is skillGuards for an embedder skill: the base
// rule over g (topical requires; a synth distribution's
// capability:synth), plus an atomic skill's own operator and that
// operator's DependsOn — every entry is an AND edge — transitively.
func extensionSkillGuards(g *OntologyGraph, md skills.Metadata, deps map[string][]string) map[string]bool {
	guards := skillGuards(g, md.Name)
	var walk func(name string)
	walk = func(name string) {
		if _, ext := deps[name]; !ext {
			hardDependencies(name, guards) // a built-in feature
			return
		}
		if guards[name] {
			return
		}
		guards[name] = true
		for _, d := range deps[name] {
			walk(d)
		}
	}
	if md.Operator != "" {
		walk(md.Operator)
	}
	return guards
}
