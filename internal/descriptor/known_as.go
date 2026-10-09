package descriptor

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/frankbardon/pulse/descriptor"
)

// KnownAsMax caps one Purpose.KnownAs alias, in characters. An alias is
// a name ("anova", "chi-square test"), never a sentence.
const KnownAsMax = 48

// FoldAlias is the comparison form of a Purpose.KnownAs alias or an
// intent Sound: lower-cased, surrounding whitespace trimmed and inner
// whitespace runs collapsed to one space. Punctuation is kept, so
// "chi-square" and "chi square" are distinct aliases. Uniqueness, the
// Sounds collision rule and the instance alias index all compare in
// this form.
func FoldAlias(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// foldedSounds maps every intent Sound, folded, to its intent ID.
func foldedSounds() map[string]string {
	out := map[string]string{}
	for _, in := range intentRegistry {
		for _, s := range in.Sounds {
			out[FoldAlias(s)] = in.ID
		}
	}
	return out
}

// knownAsViolations checks one Purpose's own aliases: each non-empty,
// at most KnownAsMax characters, listed once (folded), and no alias the
// same as an intent Sound. Uniqueness ACROSS operators is a registry
// property (KnownAsCollisions).
func knownAsViolations(name string, aliases []string) []PurposeViolation {
	var out []PurposeViolation
	bad := func(format string, args ...any) {
		out = append(out, PurposeViolation{Name: name, Rule: PurposeRuleKnownAs, Detail: fmt.Sprintf(format, args...)})
	}
	sounds := foldedSounds()
	seen := map[string]bool{}
	for i, a := range aliases {
		f := FoldAlias(a)
		switch {
		case f == "":
			bad("KnownAs[%d] is empty", i)
			continue
		case utf8.RuneCountInString(a) > KnownAsMax:
			bad("KnownAs[%d] is %d characters, limit %d", i, utf8.RuneCountInString(a), KnownAsMax)
		}
		if seen[f] {
			bad("KnownAs %q listed twice", a)
		}
		seen[f] = true
		if id, ok := sounds[f]; ok {
			bad("KnownAs %q collides with a Sound of intent %q", a, id)
		}
	}
	return out
}

// KnownAsCollisions returns a violation for every alias two operators
// of reg both declare (folded), in sorted alias order, naming the
// alphabetically later operator. TestPurposeKnownAsUnique runs it over
// builtinPurposes.
func KnownAsCollisions(reg map[string]descriptor.Purpose) []PurposeViolation {
	owners := map[string][]string{}
	for name, p := range reg {
		seen := map[string]bool{}
		for _, a := range p.KnownAs {
			f := FoldAlias(a)
			if f == "" || seen[f] {
				continue
			}
			seen[f] = true
			owners[f] = append(owners[f], name)
		}
	}
	aliases := make([]string, 0, len(owners))
	for f, names := range owners {
		if len(names) > 1 {
			aliases = append(aliases, f)
		}
	}
	sort.Strings(aliases)
	var out []PurposeViolation
	for _, f := range aliases {
		names := owners[f]
		sort.Strings(names)
		for _, n := range names[1:] {
			out = append(out, PurposeViolation{Name: n, Rule: PurposeRuleKnownAs,
				Detail: fmt.Sprintf("KnownAs %q is also declared by %s", f, names[0])})
		}
	}
	return out
}

// BuiltinKnownAs returns every built-in alias, folded, mapped to the
// operator declaring it — the full registry, profile-blind. Extension
// validation reads it so an embedder alias never shadows a built-in.
func BuiltinKnownAs() map[string]string {
	return knownAsIndex(builtinPurposes, nil)
}

// knownAsIndex folds reg's aliases into alias → operator, keeping only
// the operators keep admits (nil keeps all). On a collision the
// alphabetically first operator wins, so the index is deterministic
// even over an unvalidated registry.
func knownAsIndex(reg map[string]descriptor.Purpose, keep func(name string) bool) map[string]string {
	out := map[string]string{}
	for name, p := range reg {
		if keep != nil && !keep(name) {
			continue
		}
		for _, a := range p.KnownAs {
			f := FoldAlias(a)
			if f == "" {
				continue
			}
			if cur, ok := out[f]; !ok || name < cur {
				out[f] = name
			}
		}
	}
	return out
}

// KnownAs returns the instance's alias index: every Purpose.KnownAs
// alias (folded with FoldAlias) mapped to the operator declaring it,
// over the built-in Purposes plus the instance's extension Purposes.
// An operator the instance ontology prunes — hidden by the feature
// profile — contributes nothing, so its aliases act never-declared.
// A fresh map; nil-safe (a nil snapshot answers the full built-in
// index).
func (s *InstanceSnapshot) KnownAs() map[string]string {
	g := s.Ontology()
	keep := func(name string) bool {
		return g.Has(OntologyID(descriptor.OntologyNodeOperator, name))
	}
	out := knownAsIndex(builtinPurposes, keep)
	if ext := s.Extensions(); ext != nil {
		for f, name := range knownAsIndex(ext.Purposes, keep) {
			if _, taken := out[f]; !taken {
				out[f] = name
			}
		}
	}
	return out
}
