package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

// budgetStream: two packages, one test exactly at the budget, one just
// under it, one subtest over it (subtests are not budgeted) and a skipped
// test whose Elapsed must not count.
const budgetStream = `{"Action":"run","Package":"p/a","Test":"TestAt"}
{"Action":"pass","Package":"p/a","Test":"TestAt","Elapsed":1}
{"Action":"run","Package":"p/a","Test":"TestUnder"}
{"Action":"pass","Package":"p/a","Test":"TestUnder","Elapsed":0.99}
{"Action":"pass","Package":"p/a","Test":"TestParent/slow","Elapsed":5}
{"Action":"pass","Package":"p/a","Test":"TestParent","Elapsed":0.2}
{"Action":"skip","Package":"p/a","Test":"TestSkipped","Elapsed":7}
{"Action":"output","Package":"p/a","Output":"PASS\n"}
{"Action":"output","Package":"p/a","Output":"testing: warning: no tests to run\n"}
{"Action":"output","Package":"p/a","Output":"ok  \tp/a\t1.0s\n"}
{"Action":"pass","Package":"p/a","Elapsed":1}
{"Action":"pass","Package":"p/b","Test":"TestFast","Elapsed":0.01}
{"Action":"output","Package":"p/b","Output":"ok  \tp/b\t0.1s\n"}
{"Action":"pass","Package":"p/b","Elapsed":0.1}
`

const failingStream = `{"Action":"output","Package":"p/c","Test":"TestBroken","Output":"    c_test.go:3: nope\n"}
{"Action":"fail","Package":"p/c","Test":"TestBroken","Elapsed":0.01}
{"Action":"output","Package":"p/c","Output":"FAIL\tp/c\t0.1s\n"}
{"Action":"fail","Package":"p/c","Elapsed":0.1}
`

const buildFailStream = `{"ImportPath":"p/d","Action":"build-output","Output":"d.go:1: syntax error\n"}
{"Action":"output","Package":"p/d","Output":"FAIL\tp/d [build failed]\n"}
{"Action":"fail","Package":"p/d","Elapsed":0,"FailedBuild":"p/d"}
`

func TestBudgetReport_ThresholdEdge(t *testing.T) {
	t.Parallel()
	res := filter(strings.NewReader(budgetStream), nil, &bytes.Buffer{})
	text, n := budgetReport(res.timings, time.Second, nil)
	if n != 1 {
		t.Fatalf("offenders = %d, want 1 (TestAt at exactly 1s; 0.99s, subtests and skips are under or exempt):\n%s", n, text)
	}
	for _, want := range []string{
		"1 top-level test(s) took 1s or more",
		"  1.00s  p/a  TestAt\n",
		"move to a *_heavy_test.go file (//go:build heavy) or make it cheaper",
		"slowest 4 (margin to 1s)",
		"  0.99s   +0.01s  p/a  TestUnder\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("report lacks %q:\n%s", want, text)
		}
	}
	for _, bad := range []string{"TestSkipped", "TestParent/slow"} {
		if strings.Contains(text, bad) {
			t.Errorf("report names %s:\n%s", bad, text)
		}
	}
	if _, n := budgetReport(res.timings, 1001*time.Millisecond, nil); n != 0 {
		t.Fatalf("1.001s budget: %d offenders, want 0", n)
	}
}

func TestBudgetReport_SlowestFive(t *testing.T) {
	t.Parallel()
	var ts []timing
	for i := 0; i < 7; i++ {
		ts = append(ts, timing{pkg: "p", test: "T" + string(rune('a'+i)), secs: float64(i) / 10})
	}
	text, n := budgetReport(ts, time.Second, nil)
	if n != 0 || !strings.Contains(text, "every top-level test ran in under 1s") || !strings.Contains(text, "slowest 5") ||
		!strings.Contains(text, "  0.60s   +0.40s  p  Tg\n") || strings.Contains(text, "  Tb\n") {
		t.Fatalf("report:\n%s", text)
	}
}

func TestFilterMain_ExitStatus(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		stream string
		args   []string
		want   int
		log    string
	}{
		{"no budget never fails", failingStream + budgetStream, nil, 0, "FAIL\tp/c"},
		{"budget offender fails", budgetStream, []string{"-budget", "1s"}, 1, "TestAt"},
		{"budget met passes", budgetStream, []string{"-budget", "2s"}, 0, "ran in under 2s"},
		{"test failure passes through", failingStream, []string{"-budget", "1s"}, 1, "c_test.go:3: nope"},
		{"build failure passes through", buildFailStream, []string{"-budget", "1s"}, 1, "[build failed]"},
		{"report-only never fails", failingStream + budgetStream, []string{"-budget", "1s", "-report-only"}, 0, "TestAt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			if got := filterMain(tc.args, strings.NewReader(tc.stream), &out); got != tc.want {
				t.Fatalf("exit = %d, want %d\n%s", got, tc.want, out.String())
			}
			if strings.Contains(out.String(), "PASS\n") || strings.Contains(out.String(), "no tests to run") {
				t.Fatalf("log keeps a line non-verbose go test hides:\n%s", out.String())
			}
			if !strings.Contains(out.String(), tc.log) {
				t.Fatalf("log lacks %q:\n%s", tc.log, out.String())
			}
		})
	}
}

func TestBudgetReport_Recheck(t *testing.T) {
	t.Parallel()
	res := filter(strings.NewReader(budgetStream), nil, &bytes.Buffer{})
	fixed := func(secs float64, err error) recheckFunc {
		return func(off []timing) (map[string]float64, error) {
			if len(off) != 1 || off[0].test != "TestAt" {
				t.Errorf("recheck got %v, want only TestAt", off)
			}
			return map[string]float64{"p/a\x00TestAt": secs}, err
		}
	}
	for _, tc := range []struct {
		name  string
		rc    recheckFunc
		want  int
		lines []string
	}{
		{"cleared alone", fixed(0.4, nil), 0, []string{"p/a TestAt took 1.00s, 0.40s alone: contention, not counted", "every top-level test ran in under 1s"}},
		{"confirmed alone", fixed(1.2, nil), 1, []string{"  1.20s  p/a  TestAt\n", "move to a *_heavy_test.go file"}},
		{"rerun error keeps first time", fixed(0, errors.New("boom")), 1, []string{"isolated rerun failed (boom)", "  1.00s  p/a  TestAt\n"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			text, n := budgetReport(res.timings, time.Second, tc.rc)
			if n != tc.want {
				t.Fatalf("offenders = %d, want %d:\n%s", n, tc.want, text)
			}
			for _, l := range tc.lines {
				if !strings.Contains(text, l) {
					t.Errorf("report lacks %q:\n%s", l, text)
				}
			}
		})
	}
	called := false
	if _, n := budgetReport(res.timings, 2*time.Second, func([]timing) (map[string]float64, error) { called = true; return nil, nil }); n != 0 || called {
		t.Fatalf("no offender: n=%d, recheck called=%v; want 0 and no rerun", n, called)
	}
}
