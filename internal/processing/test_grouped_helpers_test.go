package processing

import (
	"context"

	"github.com/frankbardon/pulse/types"
)

// processBuffered runs req on the BUFFERED path over recs whatever
// canStream says — the arm the streaming-vs-buffered parity suites
// used to force by grouping a tests request, before a grouped request
// ran every test once per group (U46 E3-S1).
func processBuffered(p *Processor, req *types.Request, recs []*Record) (*types.Response, error) {
	resp, err := p.processRecords(context.Background(), p.prepare(req), recs)
	if err == nil {
		p.lastPath = PathBuffered
	}
	return resp, err
}
