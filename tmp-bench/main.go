package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"runtime"
	"runtime/pprof"
	"time"

	"github.com/frankbardon/pulse"
)

func main() {
	reqPath := flag.String("req", "", "request JSON path")
	cpuOut := flag.String("cpu", "", "cpu profile output")
	memOut := flag.String("mem", "", "heap profile output (after run)")
	allocOut := flag.String("alloc", "", "alloc profile output")
	iters := flag.Int("iters", 1, "iterations")
	flag.Parse()
	if *reqPath == "" {
		log.Fatal("--req required")
	}

	raw, err := os.ReadFile(*reqPath)
	if err != nil {
		log.Fatal(err)
	}
	var req pulse.Request
	if err := json.Unmarshal(raw, &req); err != nil {
		log.Fatal(err)
	}

	p, err := pulse.New(pulse.Options{DataDir: "/"})
	if err != nil {
		log.Fatal(err)
	}

	if *cpuOut != "" {
		f, err := os.Create(*cpuOut)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		if err := pprof.StartCPUProfile(f); err != nil {
			log.Fatal(err)
		}
		defer pprof.StopCPUProfile()
	}

	var totalDur time.Duration
	var ms0, ms1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&ms0)

	ctx := context.Background()
	for i := 0; i < *iters; i++ {
		start := time.Now()
		resp, err := p.Process(ctx, &req)
		dur := time.Since(start)
		totalDur += dur
		if err != nil {
			log.Fatalf("iter %d: %v", i, err)
		}
		if resp == nil || resp.Crosstab == nil {
			log.Fatal("no crosstab in resp")
		}
		mat := resp.Crosstab.Matrix
		fmt.Fprintf(os.Stderr, "iter %d: %s rows=%d cols=%d\n",
			i, dur, len(mat.RowKeys), len(mat.ColumnKeys))
	}
	runtime.ReadMemStats(&ms1)

	fmt.Fprintf(os.Stderr, "\n== summary ==\n")
	fmt.Fprintf(os.Stderr, "iters: %d\n", *iters)
	fmt.Fprintf(os.Stderr, "total:   %s\n", totalDur)
	fmt.Fprintf(os.Stderr, "avg:     %s\n", totalDur/time.Duration(*iters))
	fmt.Fprintf(os.Stderr, "TotalAlloc delta:  %d MB\n", (ms1.TotalAlloc-ms0.TotalAlloc)/1024/1024)
	fmt.Fprintf(os.Stderr, "Mallocs delta:     %d\n", ms1.Mallocs-ms0.Mallocs)
	fmt.Fprintf(os.Stderr, "Sys:               %d MB\n", ms1.Sys/1024/1024)
	fmt.Fprintf(os.Stderr, "HeapInuse:         %d MB\n", ms1.HeapInuse/1024/1024)

	if *allocOut != "" {
		f, _ := os.Create(*allocOut)
		_ = pprof.Lookup("allocs").WriteTo(f, 0)
		f.Close()
	}
	if *memOut != "" {
		runtime.GC()
		f, _ := os.Create(*memOut)
		_ = pprof.Lookup("heap").WriteTo(f, 0)
		f.Close()
	}
}
