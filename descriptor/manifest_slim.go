package descriptor

// SlimManifest returns a copy of m with every prose Description field
// blanked. Structural metadata (names, params, types, tiers, accept-type
// lists, streamability flags, cross-references, fixups) is preserved.
//
// The slim variant is intended for size-sensitive MCP/CLI clients that
// would rather pay a few extra discovery round-trips than transit the
// full ~70 kB descriptive payload at session start. The default manifest
// surface remains the full one; --slim is opt-in.
//
// SlimManifest never mutates the input; it shallow-copies the top-level
// struct, then per-slice shallow-copies and zeroes the Description
// fields.
func SlimManifest(m *Manifest) *Manifest {
	if m == nil {
		return nil
	}
	out := *m

	// Component slices: clear Operator.Description and per-Param
	// Description.
	slimOps := func(in []Operator) []Operator {
		dst := make([]Operator, len(in))
		for i, op := range in {
			op.Description = ""
			op.StreamableHint = ""
			op.EmitsTypeNote = ""
			if len(op.Params) > 0 {
				params := make([]Param, len(op.Params))
				for j, p := range op.Params {
					p.Description = ""
					params[j] = p
				}
				op.Params = params
			}
			dst[i] = op
		}
		return dst
	}
	out.Components = Components{
		Aggregators: slimOps(m.Components.Aggregators),
		Attributes:  slimOps(m.Components.Attributes),
		Filterers:   slimOps(m.Components.Filterers),
		Groupers:    slimOps(m.Components.Groupers),
		Windows:     slimOps(m.Components.Windows),
		Features:    slimOps(m.Components.Features),
	}

	slimTests := func(in []TestMeta) []TestMeta {
		dst := make([]TestMeta, len(in))
		for i, t := range in {
			t.Description = ""
			if len(t.Params) > 0 {
				params := make([]Param, len(t.Params))
				for j, p := range t.Params {
					p.Description = ""
					params[j] = p
				}
				t.Params = params
			}
			dst[i] = t
		}
		return dst
	}
	out.Tests = slimTests(m.Tests)
	out.PostTests = slimTests(m.PostTests)

	slimDists := make([]DistributionMeta, len(m.SynthDistributions))
	for i, d := range m.SynthDistributions {
		d.Description = ""
		if len(d.Params) > 0 {
			params := make([]Param, len(d.Params))
			for j, p := range d.Params {
				p.Description = ""
				params[j] = p
			}
			d.Params = params
		}
		slimDists[i] = d
	}
	out.SynthDistributions = slimDists

	// ErrorCodes is already name-only in the full manifest (slim
	// payload removes no further information). Shallow-copy to keep
	// the slim variant independent of the input.
	out.ErrorCodes = append([]string(nil), m.ErrorCodes...)
	out.ErrorDomains = append([]string(nil), m.ErrorDomains...)

	slimTools := make([]MCPTool, len(m.MCPTools))
	for i, mt := range m.MCPTools {
		mt.Description = ""
		slimTools[i] = mt
	}
	out.MCPTools = slimTools

	slimSkills := make([]SkillMeta, len(m.Skills))
	for i, s := range m.Skills {
		s.Description = ""
		slimSkills[i] = s
	}
	out.Skills = slimSkills

	slimCmds := make([]Command, len(m.Commands))
	for i, c := range m.Commands {
		c.Description = ""
		slimCmds[i] = c
	}
	out.Commands = slimCmds

	return &out
}
