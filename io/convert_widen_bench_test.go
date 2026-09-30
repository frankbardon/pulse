package io

import (
	"context"
	"fmt"
	"testing"
)

// BenchmarkConvertInferred measures convert's row loop over an inferred
// schema with integer, float and categorical columns — the cells the
// width check now parses.
func BenchmarkConvertInferred(b *testing.B) {
	cols := []string{"id", "qty", "price", "score", "parent"}
	rows := make([][]string, 50000)
	for i := range rows {
		rows[i] = []string{fmt.Sprint(i % 60000), fmt.Sprint(i % 9), fmt.Sprintf("%d.25", i%1000), fmt.Sprintf("%d.5", i%77), fmt.Sprintf("p%03d", i%150)}
	}
	b.ReportAllocs()
	for b.Loop() {
		job := NewConvertJob(newMockReader(cols, rows), &discardWriter{})
		if _, err := job.Run(context.Background()); err != nil {
			b.Fatal(err)
		}
	}
}

type discardWriter struct{}

func (discardWriter) WriteHeader([]string) error { return nil }
func (discardWriter) WriteRow([]any) error      { return nil }
func (discardWriter) Close() error              { return nil }
