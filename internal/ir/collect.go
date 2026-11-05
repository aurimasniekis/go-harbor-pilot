package ir

import "strings"

// Collect assembles a complete response from a stream of events. It returns
// the stream's error when one ends it.
func Collect(events <-chan Event) (*Response, error) {
	r := &Response{}
	parts := map[int]*Part{}
	var order []int
	builders := map[int]*strings.Builder{}
	var failure *Error
	for e := range events {
		switch e.Kind {
		case EvStart:
			r.ID, r.Model = e.ResponseID, e.Model
		case EvPartStart:
			if e.Part == nil {
				continue
			}
			p := *e.Part
			if p.ToolCall != nil {
				tc := *p.ToolCall
				p.ToolCall = &tc
			}
			if p.Reasoning != nil {
				rs := *p.Reasoning
				p.Reasoning = &rs
			}
			parts[e.Index] = &p
			order = append(order, e.Index)
			builders[e.Index] = &strings.Builder{}
		case EvDelta:
			if b := builders[e.Index]; b != nil {
				b.WriteString(e.Delta)
			}
		case EvPartStop:
			if e.Part != nil && e.Part.Sealed != nil {
				if p := parts[e.Index]; p != nil {
					s := *e.Part.Sealed
					p.Sealed = &s
				}
			}
		case EvUsage:
			if e.Usage != nil {
				r.Usage.Merge(*e.Usage)
			}
		case EvStop:
			r.StopReason, r.StopSeq = e.StopReason, e.StopSeq
		case EvError:
			failure = e.Err
		}
	}
	for _, i := range order {
		p := parts[i]
		text := builders[i].String()
		switch {
		case p.ToolCall != nil:
			p.ToolCall.Arguments += text
		case p.Reasoning != nil:
			p.Reasoning.Text += text
		default:
			p.Text += text
		}
		r.Parts = append(r.Parts, *p)
	}
	if failure != nil {
		return nil, failure
	}
	return r, nil
}
