package skills

import (
	"io/fs"
	"sort"
	"sync"
)

// Virtual skills are skills rendered from a Go registry instead of read
// from an embedded *.md file. They join the embedded pack through List
// and Get, so every skill surface (CLI, MCP tools and resources, the
// manifest's skills list) serves them with no per-consumer code.
//
// The registries live in packages that import this one, so the
// renderers are registered at their init time (RegisterVirtual) rather
// than called from here. The stems are reserved: an embedded *.md file
// with a reserved stem is refused by TestVirtualSkillStemsNotEmbedded
// and by RegisterVirtual itself.

// The reserved virtual skill stems.
const (
	// VirtualGlossary is the plain-language glossary skill.
	VirtualGlossary = "glossary"
	// VirtualIntents is the intent taxonomy skill.
	VirtualIntents = "intents"
)

// KindReference is the Metadata.Kind of a virtual skill: a reference
// rendered from a registry, never an operator, tool, type or design
// file, so the atomic gates and budgets do not apply to it.
const KindReference = "reference"

// ReservedVirtualNames returns the reserved virtual skill stems, sorted.
func ReservedVirtualNames() []string {
	return []string{VirtualGlossary, VirtualIntents}
}

// Virtual is one registry-rendered skill: its listed metadata and the
// renderer that produces its full markdown (frontmatter included, the
// same shape Get returns for an embedded file).
type Virtual struct {
	Metadata Metadata
	Render   func() string
}

var (
	virtualMu sync.RWMutex
	virtuals  = map[string]Virtual{}
)

// RegisterVirtual adds a virtual skill. It panics on a name that is not
// reserved, already registered, or shadowed by an embedded file — each
// a programming error that must fail loudly at init.
func RegisterVirtual(v Virtual) {
	name := v.Metadata.Name
	if !isReserved(name) {
		panic("skills: virtual skill " + name + " is not a reserved virtual stem")
	}
	if embedded(name) {
		panic("skills: virtual skill " + name + " collides with embedded " + name + ".md")
	}
	if v.Render == nil {
		panic("skills: virtual skill " + name + " has no renderer")
	}
	virtualMu.Lock()
	defer virtualMu.Unlock()
	if _, dup := virtuals[name]; dup {
		panic("skills: virtual skill " + name + " registered twice")
	}
	v.Metadata.Kind = KindReference
	virtuals[name] = v
}

// IsVirtual reports whether name is a registered virtual skill.
func IsVirtual(name string) bool {
	virtualMu.RLock()
	defer virtualMu.RUnlock()
	_, ok := virtuals[name]
	return ok
}

func isReserved(name string) bool {
	for _, r := range ReservedVirtualNames() {
		if r == name {
			return true
		}
	}
	return false
}

func embedded(name string) bool {
	_, err := fs.Stat(content, name+".md")
	return err == nil
}

// virtualMetadata returns the registered virtual skills' metadata, sorted
// by name.
func virtualMetadata() []Metadata {
	virtualMu.RLock()
	defer virtualMu.RUnlock()
	out := make([]Metadata, 0, len(virtuals))
	for _, v := range virtuals {
		out = append(out, v.Metadata)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func virtualBody(name string) (string, bool) {
	virtualMu.RLock()
	v, ok := virtuals[name]
	virtualMu.RUnlock()
	if !ok {
		return "", false
	}
	return v.Render(), true
}
