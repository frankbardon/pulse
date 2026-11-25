package pulse_test

// Shared fixture for the runnable GoDoc examples (example_*_test.go):
// a tiny sales cohort built in memory, so every example is hermetic —
// no PULSE_DATA_DIR, no disk, no network — and its output is the same
// on every architecture (small integers only).

import (
	"context"
	"fmt"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/spf13/afero"
)

// exampleSchema is the tiny cohort's schema: an order id, two
// categorical labels and an integer amount.
func exampleSchema() encoding.Schema {
	return encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, Description: "Order number, unique per row."},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Description: "Sales region."},
		{Name: "channel", Type: encoding.FieldTypeCategoricalU8, Description: "Sales channel."},
		{Name: "amount", Type: encoding.FieldTypeU32, Description: "Order amount in whole units."},
	}}
}

// exampleRows are the tiny cohort's six orders, in schema field order.
var exampleRows = []pulse.CohortRow{
	{uint64(1), "north", "web", uint64(10)},
	{uint64(2), "south", "store", uint64(20)},
	{uint64(3), "north", "store", uint64(5)},
	{uint64(4), "east", "web", uint64(15)},
	{uint64(5), "south", "web", uint64(30)},
	{uint64(6), "north", "web", uint64(25)},
}

// exampleCSV is the same six orders as CSV text, for the import example.
const exampleCSV = "id,region,channel,amount\n" +
	"1,north,web,10\n2,south,store,20\n3,north,store,5\n" +
	"4,east,web,15\n5,south,web,30\n6,north,web,25\n"

// newExampleEngine returns an engine over a fresh in-memory filesystem
// holding the tiny cohort at "sales.pulse". It panics on failure: the
// fixture is fixed, so a failure is a bug in the example itself.
func newExampleEngine(opts pulse.Options) (*pulse.Pulse, afero.Fs) {
	fsys := afero.NewMemMapFs()
	opts.FS = fsys
	p, err := pulse.New(opts)
	if err != nil {
		panic(fmt.Sprintf("pulse.New: %v", err))
	}
	writeExampleCohort(p, "sales.pulse")
	return p, fsys
}

// writeExampleCohort builds the tiny cohort at target through the
// public cohort builder.
func writeExampleCohort(p *pulse.Pulse, target string) {
	b, err := p.NewCohortBuilder(context.Background(), target, exampleSchema(), pulse.CohortBuilderOptions{})
	if err != nil {
		panic(fmt.Sprintf("NewCohortBuilder: %v", err))
	}
	for _, r := range exampleRows {
		if err := b.Append(r); err != nil {
			panic(fmt.Sprintf("Append: %v", err))
		}
	}
	if _, err := b.Close(); err != nil {
		panic(fmt.Sprintf("Close: %v", err))
	}
}
