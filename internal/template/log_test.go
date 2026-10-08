package template_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frankbardon/pulse/internal/template"
)

type logRec struct {
	level slog.Level
	msg   string
	attrs map[string]any
}

// recHandler captures records, and their text rendering for the privacy
// scan.
type recHandler struct {
	mu   sync.Mutex
	recs []logRec
	text bytes.Buffer
}

func (h *recHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *recHandler) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *recHandler) WithGroup(string) slog.Handler            { return h }
func (h *recHandler) Handle(ctx context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	rec := logRec{level: r.Level, msg: r.Message, attrs: map[string]any{}}
	r.Attrs(func(a slog.Attr) bool { rec.attrs[a.Key] = a.Value.Any(); return true })
	h.recs = append(h.recs, rec)
	return slog.NewTextHandler(&h.text, nil).Handle(ctx, r)
}

func (h *recHandler) take() []logRec {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := h.recs
	h.recs = nil
	return out
}

// isCode reports whether v is a bare Pulse template error code — not
// "uncoded", and not a message.
func isCode(v any) bool {
	s, ok := v.(string)
	return ok && strings.HasPrefix(s, "PULSE_TEMPLATE_") && !strings.ContainsAny(s, " :")
}

func recsWith(recs []logRec, msg string) []logRec {
	var out []logRec
	for _, r := range recs {
		if r.msg == msg {
			out = append(out, r)
		}
	}
	return out
}

// TestStore_LogsReloadOutcomes: a forced reload logs one Info outcome;
// a file that breaks logs one Warn (code only, never document bytes) on
// the walk that first sees it and not again; an automatic rescan that
// changed nothing logs nothing.
func TestStore_LogsReloadOutcomes(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "revenue.json", validTemplate("original"))
	s := newStore(t, dir)
	h := &recHandler{}
	s.SetLogger(slog.New(h))

	reload(t, s)
	recs := h.take()
	info := recsWith(recs, "pulse: templates reloaded")
	if len(info) != 1 || info[0].level != slog.LevelInfo || info[0].attrs["op"] != "template_reload" ||
		info[0].attrs["forced"] != true || info[0].attrs["templates"] != int64(1) || info[0].attrs["broken"] != int64(0) {
		t.Fatalf("forced reload: want one Info outcome, got %+v", recs)
	}
	if len(recsWith(recs, "pulse: template file broken")) != 0 {
		t.Fatalf("healthy reload logged a broken file: %+v", recs)
	}

	const sentinel = "zz_document_byte_sentinel"
	writeFile(t, dir, "revenue.json", `{"description": "`+sentinel+`", broken`)
	touch(t, path, time.Second)
	reload(t, s)
	recs = h.take()
	broken := recsWith(recs, "pulse: template file broken")
	if len(broken) != 1 || broken[0].level != slog.LevelWarn || broken[0].attrs["template"] != "revenue" ||
		broken[0].attrs["path"] != path || !isCode(broken[0].attrs["code"]) {
		t.Fatalf("newly broken file: want one coded Warn, got %+v", recs)
	}
	if info := recsWith(recs, "pulse: templates reloaded"); len(info) != 1 || info[0].attrs["newly_broken"] != int64(1) {
		t.Fatalf("broken reload outcome = %+v", info)
	}
	if strings.Contains(h.text.String(), sentinel) {
		t.Fatalf("document bytes leaked into the log: %q", h.text.String())
	}

	// Still broken, unchanged: no second Warn.
	reload(t, s)
	if broken := recsWith(h.take(), "pulse: template file broken"); len(broken) != 0 {
		t.Fatalf("an already-broken file warned again: %+v", broken)
	}

	// An automatic rescan that changed nothing is quiet.
	base := time.Now()
	clock := base
	s.SetClock(func() time.Time { return clock })
	clock = base.Add(template.RescanInterval + time.Millisecond)
	s.List()
	if recs := h.take(); len(recs) != 0 {
		t.Fatalf("an unchanged automatic rescan logged: %+v", recs)
	}

	// An automatic rescan that found a new file logs its outcome.
	writeFile(t, dir, "added.json", validTemplate("added"))
	clock = clock.Add(template.RescanInterval + time.Millisecond)
	s.List()
	info = recsWith(h.take(), "pulse: templates reloaded")
	if len(info) != 1 || info[0].attrs["forced"] != false {
		t.Fatalf("changed automatic rescan: want one unforced Info, got %+v", info)
	}
}

// TestStore_NilLoggerSilent: without SetLogger (or with nil) a reload
// reaches no handler, the default one included.
func TestStore_NilLoggerSilent(t *testing.T) {
	h := &recHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	defer slog.SetDefault(prev)

	dir := t.TempDir()
	writeFile(t, dir, "revenue.json", validTemplate("original"))
	s := newStore(t, dir)
	s.SetLogger(nil)
	reload(t, s)
	if recs := h.take(); len(recs) != 0 {
		t.Fatalf("nil logger produced records: %+v", recs)
	}
	var nilStore *template.Store
	nilStore.SetLogger(slog.New(h))
}
