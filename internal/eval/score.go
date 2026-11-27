package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/afero"
)

// ScoreRunDir scores an agent run: runDir holds one <task-id>.json per
// answered task, the agent's final submission ({"tool", "arguments"});
// other files (the transcript summaries) are ignored. Every task gets a
// report, sorted by task ID: a task with no submission fails as
// unanswered. workDir receives the fresh data directories: one shared
// by the read-only submissions, one per write-side submission. A file
// naming no task is an error.
func ScoreRunDir(ctx context.Context, fixtureDir, workDir, runDir string) ([]Report, error) {
	tasks, err := Tasks()
	if err != nil {
		return nil, err
	}
	byID := map[string]*Task{}
	for _, t := range tasks {
		byID[t.ID] = t
	}
	fs := afero.NewOsFs()
	files, err := afero.Glob(fs, filepath.Join(runDir, "*.json"))
	if err != nil {
		return nil, err
	}
	subs := map[string]Submission{}
	var bad []Report
	for _, f := range files {
		id := strings.TrimSuffix(filepath.Base(f), ".json")
		if byID[id] == nil {
			return nil, fmt.Errorf("%s names no task", f)
		}
		raw, err := afero.ReadFile(fs, f)
		if err != nil {
			return nil, err
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		var s Submission
		if err := dec.Decode(&s); err != nil || s.Tool == "" {
			if err == nil {
				err = fmt.Errorf("no tool")
			}
			bad = append(bad, Report{Task: id, Failures: []string{"submission: " + err.Error()}})
			continue
		}
		subs[id] = s
	}

	var readers []*Task
	for id, s := range subs {
		if !s.Writes() {
			readers = append(readers, byID[id])
		}
	}
	sort.Slice(readers, func(i, j int) bool { return readers[i].ID < readers[j].ID })
	var shared *Env
	if len(readers) > 0 {
		if shared, err = NewEnv(ctx, fixtureDir, filepath.Join(workDir, "shared"), readers...); err != nil {
			return nil, err
		}
	}

	reports := bad
	for _, t := range tasks {
		s, ok := subs[t.ID]
		switch {
		case !ok:
			if !hasReport(bad, t.ID) {
				reports = append(reports, Report{Task: t.ID, Failures: []string{"unanswered: no " + t.ID + ".json in the run directory"}})
			}
		case s.Writes():
			reports = append(reports, Score(ctx, fixtureDir, filepath.Join(workDir, t.ID), t, s))
		default:
			reports = append(reports, ScoreIn(ctx, shared, t, s))
		}
	}
	sort.Slice(reports, func(i, j int) bool { return reports[i].Task < reports[j].Task })
	return reports, nil
}

func hasReport(rs []Report, id string) bool {
	for _, r := range rs {
		if r.Task == id {
			return true
		}
	}
	return false
}
