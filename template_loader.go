package pulse

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/frankbardon/pulse/template"
)

// envTemplatesDir is the environment variable consulted when
// Options.TemplateDirs is empty. It carries one or more directory roots
// separated by os.PathListSeparator (':' on Unix, ';' on Windows), in
// precedence order — the same shape as PATH, so a deployment can layer a
// site override directory ahead of a shipped default without needing a
// second variable.
const envTemplatesDir = "PULSE_TEMPLATES_DIR"

// resolveTemplateDirs produces the ordered root list the template store is
// built over.
//
// The explicit option wins outright: when Options.TemplateDirs is non-empty
// the environment is never consulted, so a programmatic embedder is never
// surprised by an operator's ambient variable. Only when the option is
// empty does PULSE_TEMPLATES_DIR apply, split on os.PathListSeparator via
// filepath.SplitList.
//
// Empty option and empty (or all-blank) variable yield nil — "no template
// directories configured" is an ordinary deployment, not a fault, and the
// caller turns nil into no store at all.
//
// The returned slice is the engine's own copy, so a caller mutating the
// slice it handed to Options cannot retarget a live store.
func resolveTemplateDirs(opts *Options) []string {
	if len(opts.TemplateDirs) > 0 {
		return slices.Clone(opts.TemplateDirs)
	}
	raw := os.Getenv(envTemplatesDir)
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return filepath.SplitList(raw)
}

// loadTemplateStore builds the request-template store from the configured
// roots. It is called by New, eagerly, so every discovered document is
// read, parsed, and declaration-validated at startup: a malformed template
// fails pulse.New with the offending path named rather than lying in wait
// until someone renders it.
//
// No configured roots yields a nil store and a nil error. A nil *Store is
// usable — it lists nothing and answers every lookup with
// PULSE_TEMPLATE_NOT_FOUND — so the unconfigured case needs no nil checks
// downstream.
//
// Directory-shape behaviour lives in the store, not here: a blank root is
// skipped, a root that does not exist is skipped (an absent optional
// override layer is not a fault), and a root that exists but is not a
// directory is a DATA_FILE error naming the path.
//
// Like the label- and range-table loaders, this deliberately reads through
// the os package rather than afero. Template directories are process
// configuration, not cohort data.
func loadTemplateStore(opts *Options) (*template.Store, error) {
	dirs := resolveTemplateDirs(opts)
	if len(dirs) == 0 {
		return nil, nil
	}
	return template.NewStore(dirs)
}
