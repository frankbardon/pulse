// Command linkcheck HEAD-checks every external http(s) link in the
// documentation sources TestDocsLinksResolve covers (docs/src/** and the
// repo-root *.md files). `make linkcheck-external` runs it. It needs the
// network, so it is manual only and never wired into CI; internal links are
// the test's job.
//
//	go run ./internal/tools/linkcheck [-timeout 15s] [-workers 8]
//
// A URL is reported broken when the server answers 400 or above, or the
// request fails. A HEAD answered 403, 405 or 501 is retried as GET, since
// many servers refuse HEAD. Exit status 1 when any link is broken. It is a
// development tool only (never imported, never shipped).
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/internal/doccheck"
)

func main() {
	timeout := flag.Duration("timeout", 15*time.Second, "per-request timeout")
	workers := flag.Int("workers", 8, "concurrent requests")
	flag.Parse()
	broken, err := run(afero.NewOsFs(), &http.Client{Timeout: *timeout}, *workers, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "linkcheck:", err)
		os.Exit(2)
	}
	if broken > 0 {
		os.Exit(1)
	}
}

// use is one place an external URL appears.
type use struct {
	file string
	line int
}

// run collects the external links under fsys's root, checks each distinct
// URL once and prints one line per broken URL with every place it appears.
// It returns the broken count.
func run(fsys afero.Fs, client *http.Client, workers int, out io.Writer) (int, error) {
	files, err := doccheck.DocSources(fsys)
	if err != nil {
		return 0, err
	}
	uses := map[string][]use{}
	for _, f := range files {
		raw, err := afero.ReadFile(fsys, f)
		if err != nil {
			return 0, err
		}
		flavor := doccheck.GitHub
		if strings.HasPrefix(f, doccheck.BookRoot+"/") {
			flavor = doccheck.MDBook
		}
		for _, l := range doccheck.ScanMarkdown(raw, flavor).Links {
			if strings.HasPrefix(l.Target, "http://") || strings.HasPrefix(l.Target, "https://") {
				u, _, _ := strings.Cut(l.Target, "#")
				uses[u] = append(uses[u], use{file: f, line: l.Line})
			}
		}
	}
	urls := make([]string, 0, len(uses))
	for u := range uses {
		urls = append(urls, u)
	}
	sort.Strings(urls)

	if workers < 1 {
		workers = 1
	}
	results := make([]string, len(urls))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				results[i] = check(client, urls[i])
			}
		}()
	}
	for i := range urls {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	broken := 0
	for i, u := range urls {
		if results[i] == "" {
			continue
		}
		broken++
		fmt.Fprintf(out, "BROKEN %s: %s\n", u, results[i])
		for _, at := range uses[u] {
			fmt.Fprintf(out, "  %s:%d\n", at.file, at.line)
		}
	}
	fmt.Fprintf(out, "checked %d external URLs in %d files: %d broken\n", len(urls), len(files), broken)
	return broken, nil
}

// check returns "" when url answers below 400, else why it is broken.
func check(client *http.Client, url string) string {
	status, err := request(client, http.MethodHead, url)
	if err == nil && (status == http.StatusForbidden || status == http.StatusMethodNotAllowed || status == http.StatusNotImplemented) {
		status, err = request(client, http.MethodGet, url)
	}
	if err != nil {
		return err.Error()
	}
	if status >= 400 {
		return fmt.Sprintf("HTTP %d", status)
	}
	return ""
}

func request(client *http.Client, method, url string) (int, error) {
	req, err := http.NewRequestWithContext(context.Background(), method, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "pulse-linkcheck")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
	resp.Body.Close()
	return resp.StatusCode, nil
}
