package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"
)

// Error codes: the errors.Code constants of codesFile, each spelled by
// its constant, which is also its wire string (PULSE_SLOT_FAILED).
const codesFile = "errors/codes.go"

var errorCodeFamily = family{
	Mode:         "error-code",
	Noun:         "error code",
	Source:       codesFile + " Code consts",
	NoNewSubsets: true,
	Gates: []string{"TestCodesHaveFixups", "TestManifestErrorCodesComplete", "TestErrorOwners_Complete", "TestErrorMessagesNameOnlyOwners",
		"TestRegisteredCodesEmittedOrReserved", "TestFixupPathsAreWireKeys", "TestDocsCoverageNoGaps", "TestNoQuotedPathVerbs"},
	Manual: []string{
		udIndex,
		".claude/reference/update-demand/cli-mcp-errors-env.md",
		refFeatures,
		"docs/src/internals/adding-error-code.md",
	},
}

// loadCodes reads every exported constant of type Code in codesFile.
func (r *repo) loadCodes() (*family, error) {
	g := r.parse(codesFile)
	if g == nil {
		return nil, fmt.Errorf("error-code: %s not found", codesFile)
	}
	f := errorCodeFamily
	for _, d := range g.file.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, s := range gd.Specs {
			vs := s.(*ast.ValueSpec)
			if id, ok := vs.Type.(*ast.Ident); !ok || id.Name != "Code" {
				continue
			}
			for _, n := range vs.Names {
				if n.IsExported() {
					f.Members = append(f.Members, member{Name: n.Name, Words: []string{n.Name}})
				}
			}
		}
	}
	f.newMember = func(name string) member {
		name = strings.ToUpper(name)
		return member{Name: name, Words: []string{name}}
	}
	return &f, nil
}

// codePrefix is a code's DOMAIN_CATEGORY head: its first two segments
// (PULSE_OVERLAY of PULSE_OVERLAY_PARAM_MISSING), or "" for a two-segment
// code.
func codePrefix(code string) string {
	parts := strings.Split(code, "_")
	if len(parts) < 3 {
		return ""
	}
	return parts[0] + "_" + parts[1]
}

// errorCodeReport is the `error-code <CODE>` checklist. Beyond the
// complete tables it lists the code-family tables: Go var / const
// declarations naming three or more codes, most of which share the
// code's DOMAIN_CATEGORY head (slotfail's regression codes, a family's
// refusal list) — a new code of the family joins them if it belongs.
func errorCodeReport(r *repo, name string, opts options) (*Report, error) {
	f, err := r.loadCodes()
	if err != nil {
		return nil, err
	}
	return r.familyReport(f, name, opts, func(c *famCtx, rep *Report) {
		if c.isNew {
			c.regen = append(c.regen, "descriptor/testdata/manifest.json")
			if !strings.Contains(c.target.Name, "_") {
				rep.Warnings = append(rep.Warnings, c.target.Name+" is not DOMAIN_CATEGORY shaped")
			}
		}
		pre := codePrefix(c.target.Name)
		if pre == "" {
			return
		}
		sec := Section{Class: "code-family-tables", Title: "Code-family tables — lists most of whose codes are " + pre + "_* (join if it belongs)"}
		for _, t := range c.tables {
			if c.complete(t) || !strings.HasSuffix(t.Path, ".go") || !(strings.HasPrefix(t.Symbol, "var ") || strings.HasPrefix(t.Symbol, "const ")) {
				continue
			}
			k := 0
			for i := range t.Has {
				if i != c.idx && strings.HasPrefix(c.ms[i].Name, pre+"_") {
					k++
				}
			}
			if k < familyTableMin || k*2 < len(t.Has) {
				continue
			}
			note := fmt.Sprintf("%d of its %d codes are %s_*", k, len(t.Has), pre)
			if t.Has[c.idx] {
				note += ", has " + c.target.Name
			}
			sec.Sites = append(sec.Sites, Site{Path: t.Path, Symbols: []Symbol{{Name: t.Symbol, Lines: t.Lines, Note: note}}})
		}
		if len(sec.Sites) > 0 {
			rep.Sections = append(rep.Sections, sec)
		}
	})
}
