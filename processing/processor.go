package processing

import (
	"context"
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Processor is the single dynamic processing engine for Pulse.
// It handles filtering, attribute computation, grouping, and aggregation
// over record iterators backed by .pulse encoded data.
type Processor struct {
	schema *encoding.Schema
}

// NewProcessor creates a new Processor for the given schema.
func NewProcessor(schema *encoding.Schema) *Processor {
	return &Processor{schema: schema}
}

// Process executes a single request against the record iterator.
func (p *Processor) Process(ctx context.Context, req *types.Request, iter RecordIterator) (*types.Response, error) {
	// Collect all records from iterator
	var allRecords []*Record
	for iter.Next() {
		allRecords = append(allRecords, iter.Record())
	}

	return p.processRecords(ctx, req, allRecords)
}

// ProcessComposed executes multiple requests against a shared record set.
func (p *Processor) ProcessComposed(ctx context.Context, composed *types.ComposedRequest, records []*Record) ([]*types.Response, error) {
	responses := make([]*types.Response, len(composed.Requests))
	for i, req := range composed.Requests {
		iter := NewSliceIterator(records)
		resp, err := p.Process(ctx, req, iter)
		if err != nil {
			return nil, fmt.Errorf("request %d: %w", i, err)
		}
		responses[i] = resp
	}
	return responses, nil
}

func (p *Processor) processRecords(ctx context.Context, req *types.Request, records []*Record) (*types.Response, error) {
	totalRows := int64(len(records))

	// Step 1: Apply filters
	filtered, err := p.applyFilters(req.Filterers, records)
	if err != nil {
		return nil, err
	}

	// Step 2: Compute attributes (adds derived fields to records)
	if err := p.applyAttributes(req.Attributes, filtered); err != nil {
		return nil, err
	}

	// Step 3: Group if needed, then aggregate
	var data []map[string]any

	if len(req.Groups) > 0 {
		data, err = p.processGrouped(req, filtered)
		if err != nil {
			return nil, err
		}
	} else {
		row, err := p.aggregate(req.Aggregations, filtered)
		if err != nil {
			return nil, err
		}
		if row != nil {
			data = []map[string]any{row}
		}
	}

	return &types.Response{
		Data: data,
		Metadata: &types.ResponseMetadata{
			TotalRows:    totalRows,
			FilteredRows: int64(len(filtered)),
		},
	}, nil
}

func (p *Processor) applyFilters(filterers []*types.Filterer, records []*Record) ([]*Record, error) {
	if len(filterers) == 0 {
		return records, nil
	}

	// Build all filter functions
	var filterFns []FilterFunc
	for _, f := range filterers {
		factory, ok := filtererRegistry[f.Type]
		if !ok {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("unknown filter type: %s", f.Type))
		}
		builder := factory()
		fn, err := builder.Build(f, p.schema)
		if err != nil {
			return nil, err
		}
		filterFns = append(filterFns, fn)
	}

	// Apply all filters (AND logic)
	var result []*Record
	for _, r := range records {
		pass := true
		for _, fn := range filterFns {
			ok, err := fn(r)
			if err != nil {
				return nil, err
			}
			if !ok {
				pass = false
				break
			}
		}
		if pass {
			result = append(result, r)
		}
	}
	return result, nil
}

func (p *Processor) applyAttributes(attrs []*types.Attribute, records []*Record) error {
	for _, attr := range attrs {
		factory, ok := attributeRegistry[attr.Type]
		if !ok {
			return errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("unknown attribute type: %s", attr.Type))
		}
		computer, err := factory(attr, p.schema)
		if err != nil {
			return err
		}
		values, err := computer.Compute(records, attr.Field)
		if err != nil {
			return err
		}

		// Inject computed values back into records under the label name
		label := attr.Label
		if label == "" {
			label = fmt.Sprintf("%s_%s", attr.Type, attr.Field)
		}
		for i, r := range records {
			if i < len(values) {
				r.values[label] = values[i]
			}
		}
	}
	return nil
}

func (p *Processor) processGrouped(req *types.Request, records []*Record) ([]map[string]any, error) {
	// Use the first group for now (single-level grouping)
	grp := req.Groups[0]
	factory, ok := grouperRegistry[grp.Type]
	if !ok {
		return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
			fmt.Sprintf("unknown group type: %s", grp.Type))
	}
	grouper, err := factory(grp, p.schema)
	if err != nil {
		return nil, err
	}

	groups, err := grouper.Group(records, grp.Field)
	if err != nil {
		return nil, err
	}

	var data []map[string]any
	for key, groupRecords := range groups {
		row, err := p.aggregate(req.Aggregations, groupRecords)
		if err != nil {
			return nil, err
		}
		if row == nil {
			row = make(map[string]any)
		}
		row[grp.Field] = key
		data = append(data, row)
	}
	return data, nil
}

func (p *Processor) aggregate(aggs []*types.Aggregation, records []*Record) (map[string]any, error) {
	if len(aggs) == 0 {
		return nil, nil
	}

	row := make(map[string]any)
	for _, agg := range aggs {
		factory, ok := aggregatorRegistry[agg.Type]
		if !ok {
			return nil, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("unknown aggregation type: %s", agg.Type))
		}
		aggregator, err := factory(agg, p.schema)
		if err != nil {
			return nil, err
		}
		val, err := aggregator.Aggregate(records, agg.Field)
		if err != nil {
			return nil, err
		}
		label := agg.Label
		if label == "" {
			label = fmt.Sprintf("%s_%s", agg.Type, agg.Field)
		}
		row[label] = val
	}
	return row, nil
}
