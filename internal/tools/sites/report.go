package main

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// Report is the checklist, rendered as text or --json.
type Report struct {
	Mode       string    `json:"mode"`
	Target     string    `json:"target,omitempty"`
	Summary    []string  `json:"summary,omitempty"`
	Warnings   []string  `json:"warnings,omitempty"`
	Sections   []Section `json:"sections"`
	Gates      []Gate    `json:"gates,omitempty"`
	Regenerate []Regen   `json:"regenerate,omitempty"`
	Manual     []string  `json:"manual,omitempty"`
	Omitted    []string  `json:"omitted,omitempty"`
}

// Section is one site class.
type Section struct {
	Class string `json:"class"`
	Title string `json:"title"`
	Hint  string `json:"hint,omitempty"`
	Sites []Site `json:"sites"`
}

// Site is one file (or new-op path template) with its symbols.
type Site struct {
	Path     string   `json:"path"`
	Coverage string   `json:"coverage,omitempty"`
	Note     string   `json:"note,omitempty"`
	Symbols  []Symbol `json:"symbols,omitempty"`
}

// Symbol is a declaration / heading inside a site.
type Symbol struct {
	Name     string `json:"name"`
	Lines    []int  `json:"lines,omitempty"`
	Coverage string `json:"coverage,omitempty"`
	Note     string `json:"note,omitempty"`
}

// Gate is a test command to rerun.
type Gate struct {
	Why     string   `json:"why"`
	Package string   `json:"package"`
	Tests   []string `json:"tests,omitempty"`
	Command string   `json:"command"`
}

// Regen is a regeneration command and the paths it rewrites.
type Regen struct {
	Command string   `json:"command,omitempty"`
	Note    string   `json:"note,omitempty"`
	Paths   []string `json:"paths"`
}

func lineRefs(ls []int) string {
	if len(ls) == 0 {
		return ""
	}
	sort.Ints(ls)
	var parts []string
	for i, l := range ls {
		if i == 4 {
			parts = append(parts, "+"+strconv.Itoa(len(ls)-4))
			break
		}
		parts = append(parts, "L"+strconv.Itoa(l))
	}
	return strings.Join(parts, ",")
}

func (rep *Report) render(w io.Writer) {
	fmt.Fprintf(w, "sites %s %s\n", rep.Mode, rep.Target)
	for _, s := range rep.Summary {
		fmt.Fprintf(w, "  %s\n", s)
	}
	for _, s := range rep.Warnings {
		fmt.Fprintf(w, "  ! %s\n", s)
	}
	for _, sec := range rep.Sections {
		fmt.Fprintf(w, "\n## %s\n", sec.Title)
		if sec.Hint != "" {
			fmt.Fprintf(w, "   (%s)\n", sec.Hint)
		}
		for _, s := range sec.Sites {
			head := "  " + s.Path
			if s.Coverage != "" {
				head += "  [" + s.Coverage + "]"
			}
			if s.Note != "" {
				head += "  — " + s.Note
			}
			fmt.Fprintln(w, head)
			for _, sym := range s.Symbols {
				line := "    · " + orAnon(sym.Name)
				if refs := lineRefs(sym.Lines); refs != "" {
					line += "  " + refs
				}
				if sym.Coverage != "" {
					line += "  [" + sym.Coverage + "]"
				}
				if sym.Note != "" {
					line += "  " + sym.Note
				}
				fmt.Fprintln(w, line)
			}
		}
	}
	if len(rep.Gates) > 0 {
		fmt.Fprintf(w, "\n## Gates to rerun\n")
		why := ""
		for _, g := range rep.Gates {
			if g.Why != why {
				why = g.Why
				fmt.Fprintf(w, "  # %s\n", why)
			}
			fmt.Fprintf(w, "  %s\n", g.Command)
		}
	}
	if len(rep.Regenerate) > 0 {
		fmt.Fprintf(w, "\n## Regenerate (never hand-edit)\n")
		for _, rg := range rep.Regenerate {
			cmd := rg.Command
			if cmd == "" {
				cmd = "(no generator)"
			}
			fmt.Fprintf(w, "  %s\n", cmd)
			if rg.Note != "" {
				fmt.Fprintf(w, "    %s\n", rg.Note)
			}
			fmt.Fprintf(w, "    rewrites: %s\n", abbreviate(rg.Paths, 6))
		}
	}
	if len(rep.Manual) > 0 {
		fmt.Fprintf(w, "\n## Read before editing (manual: contracts + update-demand slices)\n")
		for _, m := range rep.Manual {
			fmt.Fprintf(w, "  %s\n", m)
		}
	}
	for _, o := range rep.Omitted {
		fmt.Fprintf(w, "\n(%s)\n", o)
	}
}

func orAnon(s string) string {
	if s == "" {
		return "(file)"
	}
	return s
}

func abbreviate(xs []string, n int) string {
	if len(xs) <= n {
		return strings.Join(xs, ", ")
	}
	return strings.Join(xs[:n], ", ") + fmt.Sprintf(", … (+%d)", len(xs)-n)
}

// sectionsFrom groups sites into class sections in display order.
func sectionsFrom(byClass map[string][]Site) []Section {
	var ids []string
	for id := range byClass {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return classOrder(ids[i]) < classOrder(ids[j]) })
	var out []Section
	for _, id := range ids {
		c := classByID(id)
		out = append(out, Section{Class: id, Title: c.Title, Hint: c.Hint, Sites: byClass[id]})
	}
	return out
}

// staticGates resolves the per-class structural gates of the classes
// present to rerun commands.
func (r *repo) staticGates(classes map[string]bool) []Gate {
	idx := r.testIndex()
	byDir := map[string][]string{}
	for _, c := range classTable {
		if !classes[c.ID] {
			continue
		}
		for _, g := range c.Gates {
			for _, d := range idx[g] {
				byDir[d] = append(byDir[d], g)
			}
		}
	}
	return gatesFromDirs("structural gates of the site classes above", byDir)
}

func gatesFromDirs(why string, byDir map[string][]string) []Gate {
	var dirs []string
	for d := range byDir {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	var out []Gate
	for _, d := range dirs {
		ts := append([]string{}, byDir[d]...)
		sort.Strings(ts)
		ts = dedupe(ts)
		out = append(out, Gate{Why: why, Package: d, Tests: ts, Command: goTestCmd(d, ts)})
	}
	return out
}

// regens groups golden / generated paths by their regeneration command.
func (r *repo) regens(paths []string) []Regen {
	idx := map[string]int{}
	var out []Regen
	sort.Strings(paths)
	for _, p := range paths {
		cmd, note := r.regenFor(p)
		key := cmd + "\x00" + note
		i, ok := idx[key]
		if !ok {
			i = len(out)
			idx[key] = i
			out = append(out, Regen{Command: cmd, Note: note})
		}
		out[i].Paths = append(out[i].Paths, p)
	}
	return out
}
