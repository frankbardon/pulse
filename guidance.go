package pulse

import (
	"github.com/frankbardon/pulse/descriptor"
	descx "github.com/frankbardon/pulse/internal/descriptor"
)

// Glossary returns the plain-language glossary of the statistical terms
// Pulse's guidance relies on, in declaration order. Each call returns a
// fresh deep copy: mutating it cannot affect the registry. The glossary
// is static — not a feature, so no feature profile prunes it — and is
// also served as the "glossary" skill on every skill surface.
func Glossary() []descriptor.Term {
	return descx.Glossary()
}

// Intents returns the closed intent taxonomy — the kinds of question an
// analysis can answer — in declaration order. Each call returns a fresh
// deep copy: mutating it cannot affect the registry. Operator purposes
// and the manifest's intents list cite these IDs; the taxonomy is also
// served as the "intents" skill on every skill surface.
func Intents() []descriptor.Intent {
	return descx.Intents()
}
