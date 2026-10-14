package pulse

import (
	stderrors "errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
)

// envPopulationsDir is the environment variable consulted when
// Options.PopulationsDir is empty.
const envPopulationsDir = "PULSE_POPULATIONS_DIR"

// loadPopulationsFromDir walks Options.PopulationsDir (or, when empty,
// the PULSE_POPULATIONS_DIR env var) and registers every population
// file in it as an Extensions.Populations entry named by its file stem
// (brand.pulse-population.json → "brand"). The rules are the label /
// range loaders': the option beats the env var, a missing directory is
// a no-op, a path that is not a directory is an error, the walk is not
// recursive, and a name already declared programmatically is
// PULSE_EXTENSION_DUPLICATE (dirCollision) — one name, one source.
//
// The loader's documents are selected by the population suffix
// (model.PopulationSuffix), the way the table loaders select *.json:
// every other file — a label table, a cohort and its sidecars, a model
// file — is not a population and is skipped unread, so the directory
// may be shared with other config. A population file that does not
// read or decode fails New with its path named
// (PULSE_MODEL_FILE_UNREADABLE / _FILE_INVALID / _VERSION_UNSUPPORTED,
// details.registered = the name): a malformed population is a typo the
// caller must see, never a fit that silently is not there.
func loadPopulationsFromDir(opts *Options) error {
	dir := opts.PopulationsDir
	if dir == "" {
		dir = os.Getenv(envPopulationsDir)
	}
	if dir == "" {
		return nil
	}
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("pulse: populations dir %q: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("pulse: populations dir %q is not a directory", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("pulse: reading populations dir %q: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		file := e.Name()
		if !strings.HasSuffix(file, model.PopulationSuffix) {
			continue
		}
		name := strings.TrimSuffix(file, model.PopulationSuffix)
		if name == "" {
			continue
		}
		path := filepath.Join(dir, file)
		raw, err := os.ReadFile(path)
		if err != nil {
			reason := model.ReasonFileUnreadable
			if stderrors.Is(err, os.ErrNotExist) {
				reason = model.ReasonFileMissing
			}
			return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_FILE_UNREADABLE,
				fmt.Sprintf("pulse: population file %s cannot be read (%s)", path, reason),
				map[string]any{"path": path, "registered": name, "reason": reason})
		}
		// Decoded here so a bad file is refused naming its path; the
		// registered bytes are decoded again (cheaply) with every
		// programmatic entry by decodeRegisteredPopulations.
		if _, err := model.DecodePopulationFile(path, raw); err != nil {
			return registeredPopulationFault(err, name)
		}
		if _, exists := opts.Extensions.Populations[name]; exists {
			return dirCollision("population", name, path)
		}
		if opts.Extensions.Populations == nil {
			opts.Extensions.Populations = map[string]Population{}
		}
		opts.Extensions.Populations[name] = Population{File: raw}
	}
	return nil
}

// decodeRegisteredPopulations decodes every Extensions.Populations entry
// (programmatic and directory-loaded alike) in name order, the first
// fault winning: a blank name is PULSE_MODEL_REF_INVALID reason
// name_empty; a document that does not decode keeps
// DecodePopulationFile's code (PULSE_MODEL_FILE_INVALID /
// _VERSION_UNSUPPORTED), its source the registration
// (Extensions.Populations["name"]) and details.registered the name.
func decodeRegisteredPopulations(pops map[string]Population) (map[string]*model.PopulationFile, error) {
	if len(pops) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(pops))
	for n := range pops {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make(map[string]*model.PopulationFile, len(pops))
	for _, n := range names {
		if strings.TrimSpace(n) == "" {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_REF_INVALID,
				"Extensions.Populations: a registered population has no name",
				map[string]any{"registered": n, "reason": model.ReasonRefNameEmpty})
		}
		f, err := model.DecodePopulationFile("Extensions.Populations["+strconv.Quote(n)+"]", pops[n].File)
		if err != nil {
			return nil, registeredPopulationFault(err, n)
		}
		out[n] = f
	}
	return out, nil
}

// registeredPopulationFault is a population decode refusal located at
// the registered name (details.registered); its code and message stay.
func registeredPopulationFault(err error, name string) error {
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		return errors.WrapCodedError(err, errors.PULSE_MODEL_FILE_INVALID, "decoding registered population "+strconv.Quote(name))
	}
	details := make(map[string]any, len(ce.Details)+1)
	for k, v := range ce.Details {
		details[k] = v
	}
	details["registered"] = name
	return errors.NewCodedErrorWithDetails(ce.Code, ce.Message, details)
}

// registeredPopulations is the instance's registered populations, the
// visible ones only (withoutHiddenExtensions drops them all when a
// feature profile hides capability:models): the runtime set every
// request layers over and the manifest / predict projection of the
// same names and labels. decoded is decodeRegisteredPopulations'
// result over every registration.
func registeredPopulations(visible map[string]Population, decoded map[string]*model.PopulationFile) (*model.Set, []descriptor.PopulationMeta, error) {
	if len(visible) == 0 {
		return nil, nil, nil
	}
	files := make(map[string]*model.PopulationFile, len(visible))
	metas := make([]descriptor.PopulationMeta, 0, len(visible))
	for n := range visible {
		f := decoded[n]
		files[n] = f
		metas = append(metas, descriptor.PopulationMeta{
			Name:     n,
			Operator: f.Operator,
			Basis:    string(f.Basis),
			Labels:   append([]string{}, f.Labels...),
			N:        f.N,
		})
	}
	sort.Slice(metas, func(i, j int) bool { return metas[i].Name < metas[j].Name })
	set, err := model.RegisteredSet(files)
	if err != nil {
		return nil, nil, err
	}
	return set, metas, nil
}

// dirCollision is the PULSE_EXTENSION_DUPLICATE refusal of a directory
// loader (label table, range table, population) whose file names an
// entry already declared programmatically on Options.Extensions: one
// name has exactly one source of truth.
func dirCollision(category, name, path string) error {
	return errors.NewCodedErrorWithDetails(errors.PULSE_EXTENSION_DUPLICATE,
		fmt.Sprintf("pulse: %s %q declared both programmatically and on disk (%s)", strings.ReplaceAll(category, "_", " "), name, path),
		map[string]any{"category": category, "name": name, "path": path})
}
