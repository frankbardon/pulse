package pulse

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/synth"
)

// synthLookupOptions resolves the lookup tables a synth run reads
// (synth.Options.LookupTables): a caller-supplied non-nil map is used as
// given; nil takes the instance's own — Extensions.LookupTables,
// LookupTablesDir or PULSE_LOOKUP_TABLES_DIR, after feature-profile
// hiding — so the CLI's synth leaves honour the env var through
// pulse.New. A callback-backed instance table has no content to digest
// (Spec.HashWithLookups), so a spec naming one is refused
// PULSE_SYNTH_EXPR_NONDETERMINISTIC before anything runs; an unnamed one
// is simply not passed.
func (p *Pulse) synthLookupOptions(spec *SynthSpec, opts SynthOptions) (SynthOptions, error) {
	if opts.LookupTables != nil || spec == nil {
		return opts, nil
	}
	reg := p.svc.Extensions()
	if reg == nil || len(reg.LookupTables) == 0 {
		return opts, nil
	}
	named := map[string]bool{}
	for _, name := range spec.ReferencedLookupTables() {
		named[name] = true
	}
	tables := make(map[string]synth.LookupTable, len(reg.LookupTables))
	for name, t := range reg.LookupTables {
		if t.Lookup != nil {
			if named[name] {
				return opts, errors.NewCodedErrorWithDetails(errors.PULSE_SYNTH_EXPR_NONDETERMINISTIC,
					"synth spec calls lookup(\""+name+"\", ...), a callback-backed lookup table: its content cannot be digested into the spec's identity, so the output is not a function of (spec, seed, tables) — register it with Rows to read it from synth",
					map[string]any{"function": exprenv.LookupFunc, "table": name})
			}
			continue
		}
		tables[name] = synth.LookupTable{Rows: t.Rows, Axes: t.Axes}
	}
	opts.LookupTables = tables
	return opts, nil
}

// synthIdentity is a synth run's request hash: the spec under the lookup
// tables it reads (Spec.HashWithLookups), so a changed table changes the
// operation's identity.
type synthIdentity struct {
	spec   *SynthSpec
	tables map[string]synth.LookupTable
}

func (s synthIdentity) Hash() string { return s.spec.HashWithLookups(s.tables) }
