package obsprom

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/frankbardon/pulse/observe"
)

// wantExposition is the text exposition (format 0.0.4) for the
// registry exposeFixture builds, written by hand from the format spec:
// HELP then TYPE per family, families sorted by name, series by label
// set, label values escaped (\\, \", \n), histograms as cumulative
// _bucket{le} lines ending in +Inf plus _sum and _count, and an
// untouched series omitted while its family's HELP/TYPE still print.
const wantExposition = `# HELP custom_gauge Pulse metric custom_gauge.
# TYPE custom_gauge gauge
custom_gauge 1.5
# HELP pulse_hook_panics_total Observability hook panics Pulse recovered, by hook.
# TYPE pulse_hook_panics_total counter
# HELP pulse_operation_duration_seconds Pulse operation wall time in seconds, by operation kind and scope.
# TYPE pulse_operation_duration_seconds histogram
pulse_operation_duration_seconds_bucket{op="process",scope="top",le="0.1"} 2
pulse_operation_duration_seconds_bucket{op="process",scope="top",le="1"} 3
pulse_operation_duration_seconds_bucket{op="process",scope="top",le="+Inf"} 4
pulse_operation_duration_seconds_sum{op="process",scope="top"} 6.65
pulse_operation_duration_seconds_count{op="process",scope="top"} 4
# HELP pulse_operations_total Pulse operations finished, by operation kind, result code (ok or an error code) and scope (top or child).
# TYPE pulse_operations_total counter
pulse_operations_total{op="process",code="ok",scope="top"} 3
pulse_operations_total{op="we\\ird\"op\nx",code="ok",scope="top"} 1
`

func exposeFixture() *Registry {
	r := NewWithBuckets([]float64{1, 0.1})
	ok := r.Counter("pulse_operations_total",
		observe.Label{Key: "op", Value: "process"}, observe.Label{Key: "code", Value: "ok"}, observe.Label{Key: "scope", Value: "top"})
	ok.Add(2)
	ok.Add(1)
	ok.Add(-5) // ignored: counters never decrease
	r.Counter("pulse_operations_total",
		observe.Label{Key: "op", Value: "we\\ird\"op\nx"}, observe.Label{Key: "code", Value: "ok"}, observe.Label{Key: "scope", Value: "top"}).Add(1)
	// Pre-resolved but never written: omitted.
	r.Counter("pulse_operations_total",
		observe.Label{Key: "op", Value: "process"}, observe.Label{Key: "code", Value: "PULSE_LIMIT_EXCEEDED"}, observe.Label{Key: "scope", Value: "top"})
	r.Counter("pulse_hook_panics_total", observe.Label{Key: "hook", Value: "end"})

	h := r.Histogram("pulse_operation_duration_seconds", observe.Label{Key: "op", Value: "process"}, observe.Label{Key: "scope", Value: "top"})
	for _, v := range []float64{0.05, 0.1, 0.5, 6} { // 0.1 is on a bound: le is inclusive
		h.Observe(v)
	}
	g := r.UpDownCounter("custom_gauge")
	g.Add(2)
	g.Add(-0.5)
	return r
}

// TestExposition (FR-21) compares the exporter against a hand-written
// spec rendering and runs the result through a line validator.
func TestExposition(t *testing.T) {
	var b bytes.Buffer
	if err := exposeFixture().WriteText(&b); err != nil {
		t.Fatal(err)
	}
	if got := b.String(); got != wantExposition {
		t.Errorf("exposition mismatch\n--- got ---\n%s\n--- want ---\n%s", got, wantExposition)
	}
	validateExposition(t, b.String())
}

var (
	reHelp   = regexp.MustCompile(`^# HELP ([a-zA-Z_:][a-zA-Z0-9_:]*) (.*)$`)
	reType   = regexp.MustCompile(`^# TYPE ([a-zA-Z_:][a-zA-Z0-9_:]*) (counter|gauge|histogram|summary|untyped)$`)
	reSample = regexp.MustCompile(`^([a-zA-Z_:][a-zA-Z0-9_:]*)(\{(?:[a-zA-Z_][a-zA-Z0-9_]*="(?:[^"\\\n]|\\[\\"n])*"(?:,[a-zA-Z_][a-zA-Z0-9_]*="(?:[^"\\\n]|\\[\\"n])*")*)?\})? (\S+)$`)
)

// validateExposition is a small text-format checker: every line is a
// HELP, TYPE or sample line; HELP precedes TYPE once per family; each
// sample belongs to the family whose TYPE came last (histograms via
// _bucket/_sum/_count); values parse as floats; histogram buckets are
// cumulative and end with le="+Inf" equal to _count.
func validateExposition(t *testing.T, text string) {
	t.Helper()
	if text != "" && !strings.HasSuffix(text, "\n") {
		t.Fatalf("exposition must end with a newline")
	}
	var (
		fam, kind string
		helped    = map[string]bool{}
		typed     = map[string]bool{}
		lastCum   = map[string]float64{}
		infCount  = map[string]float64{}
	)
	sc := bufio.NewScanner(strings.NewReader(text))
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if m := reHelp.FindStringSubmatch(line); m != nil {
			if helped[m[1]] {
				t.Errorf("line %d: second HELP for %s", n, m[1])
			}
			helped[m[1]] = true
			continue
		}
		if m := reType.FindStringSubmatch(line); m != nil {
			if typed[m[1]] || !helped[m[1]] {
				t.Errorf("line %d: TYPE for %s duplicated or before HELP", n, m[1])
			}
			typed[m[1]] = true
			fam, kind = m[1], m[2]
			continue
		}
		m := reSample.FindStringSubmatch(line)
		if m == nil {
			t.Errorf("line %d: not a valid exposition line: %q", n, line)
			continue
		}
		name, labels := m[1], m[2]
		v, err := strconv.ParseFloat(strings.Replace(m[3], "+Inf", "Inf", 1), 64)
		if err != nil {
			t.Errorf("line %d: value %q: %v", n, m[3], err)
		}
		switch kind {
		case "histogram":
			key := regexp.MustCompile(`,?le="[^"]*"`).ReplaceAllString(labels, "")
			switch name {
			case fam + "_bucket":
				if !strings.Contains(labels, `le="`) {
					t.Errorf("line %d: bucket without le", n)
				}
				if v < lastCum[key] {
					t.Errorf("line %d: bucket not cumulative", n)
				}
				lastCum[key] = v
				if strings.Contains(labels, `le="+Inf"`) {
					infCount[key] = v
				}
			case fam + "_sum":
			case fam + "_count":
				if c, ok := infCount[key]; !ok || c != v {
					t.Errorf("line %d: _count %v != +Inf bucket %v", n, v, c)
				}
				delete(lastCum, key)
			default:
				t.Errorf("line %d: sample %s outside histogram family %s", n, name, fam)
			}
		default:
			if name != fam {
				t.Errorf("line %d: sample %s outside family %s", n, name, fam)
			}
		}
	}
}

func TestEscapingAndFormatting(t *testing.T) {
	if got := escapeLabelValue("a\\b\"c\nd"); got != `a\\b\"c\nd` {
		t.Errorf("escapeLabelValue = %q", got)
	}
	if got := escapeHelp("a\\b\nc\"d"); got != `a\\b\nc"d` {
		t.Errorf("escapeHelp = %q", got)
	}
	for v, want := range map[float64]string{math.Inf(1): "+Inf", math.Inf(-1): "-Inf", 0.25: "0.25", 3: "3", 1e21: "1e+21"} {
		if got := formatFloat(v); got != want {
			t.Errorf("formatFloat(%v) = %q, want %q", v, got, want)
		}
	}
	if formatFloat(math.NaN()) != "NaN" {
		t.Error("NaN formatting")
	}
}

func TestRegistrySemantics(t *testing.T) {
	r := New()
	a := r.Counter("x_total", observe.Label{Key: "k", Value: "v"})
	b := r.Counter("x_total", observe.Label{Key: "k", Value: "v"})
	a.Add(1)
	b.Add(1)
	var buf bytes.Buffer
	_ = r.WriteText(&buf)
	if !strings.Contains(buf.String(), `x_total{k="v"} 2`) {
		t.Errorf("same name+labels must share one series:\n%s", buf.String())
	}
	// Kind clash, invalid names, reserved label names: no-op instruments,
	// nothing registered.
	r.Histogram("x_total").Observe(1)
	r.Counter("bad-name").Add(1)
	r.Counter("y_total", observe.Label{Key: "le", Value: "1"}).Add(1)
	r.Counter("z_total", observe.Label{Key: "__x", Value: "1"}).Add(1)
	buf.Reset()
	_ = r.WriteText(&buf)
	for _, bad := range []string{"bad-name", "y_total", "z_total", "x_total_bucket"} {
		if strings.Contains(buf.String(), bad) {
			t.Errorf("%s should not be exposed:\n%s", bad, buf.String())
		}
	}
	if len(DefaultBuckets) == 0 || DefaultBuckets[0] <= 0 {
		t.Error("DefaultBuckets must be positive seconds")
	}
	for i := 1; i < len(DefaultBuckets); i++ {
		if DefaultBuckets[i] <= DefaultBuckets[i-1] {
			t.Errorf("DefaultBuckets not increasing at %d", i)
		}
	}
	validateExposition(t, buf.String())
}

func TestConcurrentWrites(t *testing.T) {
	r := New()
	c := r.Counter("c_total")
	g := r.UpDownCounter("g")
	h := r.Histogram("h_seconds")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				c.Add(1)
				g.Add(1)
				g.Add(-1)
				h.Observe(0.01)
				if j%100 == 0 {
					_ = r.WriteText(io.Discard)
				}
			}
		}()
	}
	wg.Wait()
	var buf bytes.Buffer
	_ = r.WriteText(&buf)
	for _, want := range []string{"c_total 8000\n", "g 0\n", "h_seconds_count 8000\n"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("missing %q in:\n%s", want, buf.String())
		}
	}
}

func TestServer(t *testing.T) {
	r := exposeFixture()
	s, err := Listen("127.0.0.1:0", r)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Shutdown(context.Background()) }()

	resp, err := http.Get("http://" + s.Addr() + Path)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != ContentType || string(body) != wantExposition {
		t.Errorf("GET %s: status %d, type %q, body:\n%s", Path, resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}

	resp, err = http.Get("http://" + s.Addr() + "/other")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /other: status %d, want 404", resp.StatusCode)
	}

	resp, err = http.Post("http://"+s.Addr()+Path, "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST: status %d, want 405", resp.StatusCode)
	}

	if _, err := Listen(s.Addr(), r); err == nil {
		t.Error("Listen on a busy address must fail synchronously")
	}
}
