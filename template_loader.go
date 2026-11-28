package pulse

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/frankbardon/pulse/internal/template"
	"github.com/spf13/afero"
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
// exprParams are the visible extensions' expression-capable params keys
// per operator name (ExtensionRegistry.ScalarParamsByOperator): the store
// refuses a `{{token}}` in one of them PULSE_TEMPLATE_EXPR_SPLICE, at
// startup and on every rescan, as in a built-in expression slot.
//
// The store reads through templateFs(opts): the embedder's Options.FS when
// one is supplied, so an embedder on an in-memory filesystem keeps its
// templates there, and the host filesystem otherwise.
//
// Pulse's own *.json sidecars (isPulseSidecarName) are skipped unread, as
// the label-, lookup- and range-table loaders skip them: a templates
// directory may also be a data directory, and a sidecar is not a broken
// template.
func loadTemplateStore(opts *Options, exprParams map[string][]string) (*template.Store, error) {
	dirs := resolveTemplateDirs(opts)
	if len(dirs) == 0 {
		return nil, nil
	}
	return template.NewStoreWith(templateFs(opts), dirs, template.ValidateOptions{ExprParams: exprParams, SkipName: isPulseSidecarName})
}

// templateFs is the filesystem template roots are paths on: configDirFs.
func templateFs(opts *Options) afero.Fs { return configDirFs(opts) }

// configDirFs is the filesystem every configuration directory is a path
// on — the template roots and the label-, range-, lookup-table and
// populations directories. A supplied Options.FS is used as-is, so an
// embedder on an in-memory filesystem keeps its configuration there.
// Without one the instance filesystem is rooted at the data directory,
// and these directories are process configuration — host paths, absolute
// or relative to the working directory, that need not sit under DataDir —
// so they are read from the host filesystem.
func configDirFs(opts *Options) afero.Fs {
	if opts.FS != nil {
		return opts.FS
	}
	return afero.NewOsFs()
}
