package cli

import (
	"bytes"
	"context"
	stderrors "errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	perrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/obsprom"
)

// TestNewLogger_LevelsAndFormats: off (the default) builds no logger —
// pulse.Options.Logger nil is the silent path — every named level gates
// its records, json and text both render, and a bad value is CLI_INPUT.
func TestNewLogger_LevelsAndFormats(t *testing.T) {
	for _, lv := range []string{"", "off", "OFF"} {
		lg, err := newLogger(lv, "text", io.Discard)
		if err != nil || lg != nil {
			t.Errorf("newLogger(%q) = %v, %v; want nil, nil", lv, lg, err)
		}
	}

	var buf bytes.Buffer
	lg, err := newLogger("warn", "json", &buf)
	if err != nil || lg == nil {
		t.Fatalf("newLogger(warn, json) = %v, %v", lg, err)
	}
	lg.Info("dropped")
	lg.Warn("kept", slog.String("op", "process"))
	if out := buf.String(); strings.Contains(out, "dropped") || !strings.Contains(out, `"msg":"kept"`) {
		t.Errorf("warn/json output = %q", out)
	}

	buf.Reset()
	lg, err = newLogger("debug", "", &buf)
	if err != nil || lg == nil {
		t.Fatalf("newLogger(debug, \"\") = %v, %v", lg, err)
	}
	lg.Debug("plan")
	if out := buf.String(); !strings.Contains(out, "level=DEBUG") || !strings.Contains(out, "msg=plan") {
		t.Errorf("debug/text output = %q", out)
	}

	for _, bad := range [][2]string{{"verbose", "text"}, {"info", "xml"}, {"off", "xml"}} {
		_, err := newLogger(bad[0], bad[1], io.Discard)
		var ce *perrors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != perrors.CLI_INPUT {
			t.Errorf("newLogger(%q, %q) err = %v, want CLI_INPUT", bad[0], bad[1], err)
		}
	}
}

// TestLoggerFrom_ThreadsIntoOptions: the ctx logger reaches
// pulse.Options.Logger; no logger in ctx leaves it nil; a caller's own
// logger is never replaced.
func TestLoggerFrom_ThreadsIntoOptions(t *testing.T) {
	lg := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), loggerKey{}, lg)
	if got := withLogger(ctx, newOptsForTest()).Logger; got != lg {
		t.Errorf("withLogger(ctx) Logger = %p, want %p", got, lg)
	}
	if got := withLogger(context.Background(), newOptsForTest()).Logger; got != nil {
		t.Errorf("withLogger(no logger) Logger = %p, want nil", got)
	}
	own := slog.New(slog.NewTextHandler(io.Discard, nil))
	o := newOptsForTest()
	o.Logger = own
	if got := withLogger(ctx, o).Logger; got != own {
		t.Error("withLogger replaced the caller's own logger")
	}
}

// TestStartMetrics_OnlyWhenAsked: no address opens no listener; an
// address serves the registry on /metrics; a bad address is CLI_INPUT.
func TestStartMetrics_OnlyWhenAsked(t *testing.T) {
	srv, err := startMetrics("", obsprom.New())
	if srv != nil || err != nil {
		t.Fatalf("startMetrics(\"\") = %v, %v; want no listener", srv, err)
	}

	reg := obsprom.New()
	reg.Counter("pulse_operations_total").Add(1)
	srv, err = startMetrics("127.0.0.1:0", reg)
	if err != nil || srv == nil {
		t.Fatalf("startMetrics(127.0.0.1:0) = %v, %v", srv, err)
	}
	defer func() { _ = srv.Close() }()
	resp, err := http.Get("http://" + srv.Addr() + obsprom.Path)
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "pulse_operations_total 1") {
		t.Errorf("GET /metrics = %d %q", resp.StatusCode, body)
	}

	_, err = startMetrics("not-an-address", obsprom.New())
	var ce *perrors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perrors.CLI_INPUT {
		t.Errorf("startMetrics(bad) err = %v, want CLI_INPUT", err)
	}
}

func newOptsForTest() pulse.Options { return pulse.Options{} }
