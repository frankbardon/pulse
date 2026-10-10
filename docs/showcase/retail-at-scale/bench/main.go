//go:build ignore

// Command bench times the library-only knobs of the retail-at-scale
// showcase: header-only CountRecords / Inspect, in-process point lookup,
// DecodeWorkers and projection on a single-file month, ShardWorkers on the
// annual archive, and the streaming result channel.
//
// Run it from the directory that holds the generated cohorts:
//
//	go run /path/to/pulse/docs/showcase/retail-at-scale/bench/main.go -mode month
//	go run /path/to/pulse/docs/showcase/retail-at-scale/bench/main.go -mode archive -shard-workers 4
//
// Each mode prints one JSON object. peak_rss_mb is the process high-water
// mark (VmHWM), so run one archive mode per invocation.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/types"
)

func loadReq(path, cohort string) *types.Request {
	raw, err := os.ReadFile(path)
	must(err)
	var r types.Request
	must(json.Unmarshal(raw, &r))
	if cohort != "" {
		r.Cohort = &types.Cohort{Filename: cohort, DataDir: "."}
	}
	return &r
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// best runs f n times and returns the fastest wall-clock duration.
func best(n int, f func()) time.Duration {
	var b time.Duration
	for i := 0; i < n; i++ {
		t := time.Now()
		f()
		d := time.Since(t)
		if i == 0 || d < b {
			b = d
		}
	}
	return b
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func peakRSSMB() int {
	raw, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return -1
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "VmHWM:") {
			var kb int
			fmt.Sscanf(strings.TrimSpace(strings.TrimPrefix(line, "VmHWM:")), "%d", &kb)
			return kb / 1024
		}
	}
	return -1
}

func main() {
	mode := flag.String("mode", "month", "month | archive | stream")
	month := flag.String("month", "orders-2025-12.pulse", "single-file monthly cohort")
	archive := flag.String("archive", "orders.pulse", "shard archive")
	customer := flag.String("customer", "4242", "customer_id for the in-process lookup")
	kpi := flag.String("kpi", "req/01_year_kpis.json", "KPI request (cohort is overridden)")
	trend := flag.String("trend", "req/02_monthly_trend.json", "grouped request for the stream mode")
	shardWorkers := flag.Int("shard-workers", 0, "archive mode: Options.ShardWorkers (0 = NumCPU)")
	flag.Parse()
	ctx := context.Background()
	// Options.DataDir must be absolute: a relative "." makes every open fail
	// with SERVICE_RESOURCE (the filesystem is rooted with afero.BasePathFs).
	dir, err := os.Getwd()
	must(err)
	out := map[string]any{"mode": *mode}

	switch *mode {
	case "month":
		p, err := pulse.New(pulse.Options{DataDir: dir})
		must(err)
		var n uint64
		out["count_records_ms"] = ms(best(5, func() { n, err = p.CountRecords(ctx, *month); must(err) }))
		out["count_records"] = n
		out["inspect_ms"] = ms(best(5, func() { _, err = p.Inspect(ctx, *month); must(err) }))
		var rows int
		out["lookup_ms"] = ms(best(5, func() {
			res, err := p.Lookup(ctx, &pulse.LookupRequest{
				Cohort:       &types.Cohort{Filename: *month, DataDir: "."},
				Field:        "customer_id",
				Value:        *customer,
				Multiplicity: types.LookupMultiplicityAll,
			})
			must(err)
			rows = len(res.Rows)
		}))
		out["lookup_rows"] = rows
		req := loadReq(*kpi, *month)
		decode := map[string]float64{}
		var revenue any
		for _, cfg := range []struct {
			name string
			opts pulse.Options
		}{
			{"decode_workers_1", pulse.Options{DataDir: dir, DecodeWorkers: 1}},
			{"decode_workers_2", pulse.Options{DataDir: dir, DecodeWorkers: 2}},
			{"decode_workers_4", pulse.Options{DataDir: dir, DecodeWorkers: 4}},
			{"decode_workers_4_no_projection", pulse.Options{DataDir: dir, DecodeWorkers: 4, DisableProjection: true}},
		} {
			q, err := pulse.New(cfg.opts)
			must(err)
			decode[cfg.name] = ms(best(3, func() {
				resp, err := q.Process(ctx, req)
				must(err)
				revenue = resp.Data[0]["revenue"]
			}))
		}
		out["kpi_ms"] = decode
		out["kpi_revenue"] = revenue
	case "archive":
		p, err := pulse.New(pulse.Options{DataDir: dir, ShardWorkers: *shardWorkers})
		must(err)
		req := loadReq(*kpi, *archive)
		var resp *pulse.Response
		out["shard_workers"] = *shardWorkers
		out["kpi_ms"] = ms(best(1, func() { resp, err = p.Process(ctx, req); must(err) }))
		out["kpi_lines"] = resp.Data[0]["lines"]
		out["kpi_revenue"] = resp.Data[0]["revenue"]
	case "stream":
		p, err := pulse.New(pulse.Options{DataDir: dir})
		must(err)
		req := loadReq(*trend, *archive)
		start := time.Now()
		res, err := p.ProcessStreamResult(ctx, req)
		must(err)
		var first time.Duration
		chunks := 0
		var lastLines any
		for c := range res.Chunks {
			if chunks == 0 {
				first = time.Since(start)
			}
			chunks++
			lastLines = c.Data["lines"]
		}
		term := <-res.Done
		out["first_chunk_ms"] = ms(first)
		out["total_ms"] = ms(time.Since(start))
		out["chunks"] = chunks
		out["terminal_status"] = fmt.Sprint(term.Status)
		out["terminal_total_rows"] = term.TotalRows
		out["last_chunk_lines"] = lastLines
	default:
		must(fmt.Errorf("unknown -mode %q", *mode))
	}
	out["peak_rss_mb"] = peakRSSMB()
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	must(enc.Encode(out))
}
