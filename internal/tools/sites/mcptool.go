package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"strconv"
	"strings"
)

// MCP tools: the Tool* string constants of toolMetaFile, each spelled by
// its constant (ToolInspect), its description constant (DescInspect),
// its wire name (pulse_inspect) and its atomic skill (tool-inspect).
const toolMetaFile = "internal/mcp/toolmeta/meta.go"

var mcpToolFamily = family{
	Mode:   "mcp-tool",
	Noun:   "MCP tool",
	Source: toolMetaFile + " Tool* consts",
	Gates: []string{"TestManifestMCPToolsComplete", "TestSkillsCoverAllMCPTools", "TestToolDescriptionsLeadWithWhenToUse",
		"TestToolDescriptionFences_Valid", "TestFeaturesHaveSince", "TestOperatorHasAtomicSkill", "TestBindForInstance_PayloadSchemaParity",
		"TestDocsCoverageNoGaps"},
	Manual: []string{
		udIndex,
		".claude/reference/update-demand/cli-mcp-errors-env.md",
		refFeatures,
		refSkills,
		"docs/src/internals/adding-mcp-tool.md",
	},
}

const toolPrefix = "pulse_"

func toolMember(wire string, consts ...string) member {
	m := member{Name: wire, Words: append([]string{wire}, consts...), Stems: []string{"tool-" + kebab(strings.TrimPrefix(wire, toolPrefix))}}
	return m
}

// loadTools reads the Tool* constants (and their Desc* partners).
func (r *repo) loadTools() (*family, error) {
	g := r.parse(toolMetaFile)
	if g == nil {
		return nil, fmt.Errorf("mcp-tool: %s not found", toolMetaFile)
	}
	type tc struct{ konst, wire string }
	var tools []tc
	desc := map[string]bool{}
	for _, d := range g.file.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, s := range gd.Specs {
			vs := s.(*ast.ValueSpec)
			for i, n := range vs.Names {
				switch {
				case strings.HasPrefix(n.Name, "Desc"):
					desc[n.Name] = true
				case strings.HasPrefix(n.Name, "Tool") && i < len(vs.Values):
					if bl, ok := vs.Values[i].(*ast.BasicLit); ok && bl.Kind == token.STRING {
						v, _ := strconv.Unquote(bl.Value)
						tools = append(tools, tc{n.Name, v})
					}
				}
			}
		}
	}
	f := mcpToolFamily
	for _, t := range tools {
		consts := []string{t.konst}
		if d := "Desc" + strings.TrimPrefix(t.konst, "Tool"); desc[d] {
			consts = append(consts, d)
		}
		f.Members = append(f.Members, toolMember(t.wire, consts...))
	}
	f.newMember = func(name string) member {
		name = strings.ToLower(name)
		if !strings.HasPrefix(name, toolPrefix) {
			name = toolPrefix + name
		}
		return toolMember(name)
	}
	return &f, nil
}

// mcpToolReport is the `mcp-tool <name>` checklist (pulse_inspect,
// inspect or ToolInspect).
func mcpToolReport(r *repo, name string, opts options) (*Report, error) {
	f, err := r.loadTools()
	if err != nil {
		return nil, err
	}
	if f.lookup(name) < 0 && !strings.HasPrefix(strings.ToLower(name), toolPrefix) {
		if i := f.lookup(toolPrefix + strings.ToLower(name)); i >= 0 {
			name = f.Members[i].Name
		}
	}
	return r.familyReport(f, name, opts, func(c *famCtx, rep *Report) {
		if c.isNew {
			c.regen = append(c.regen, "descriptor/testdata/manifest.json")
			for _, p := range []string{toolMetaFile, "internal/skills/" + c.target.Stems[0] + ".md"} {
				note := "create"
				if contains(r.files, p) {
					note = "add the Tool / Desc constants"
				}
				rep.Summary = append(rep.Summary, p+" — "+note)
			}
		}
	})
}
