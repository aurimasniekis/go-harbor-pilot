package protocol

import "github.com/aurimasniekis/go-harbor-pilot/internal/ir"

// serial wraps a ClientStream so output parts never overlap: while one part
// is open, events for other parts are buffered and released, in start order,
// once it stops. Upstreams that interleave parallel tool-call deltas are
// thereby rendered as the sequential blocks every client protocol expects.
// Buffering is bounded by the response size limits enforced upstream.
type serial struct {
	ClientStream
	active int
	queue  map[int][]ir.Event
	order  []int
}

func newSerial(cs ClientStream) ClientStream {
	if cs == nil {
		return nil
	}
	return &serial{ClientStream: cs, active: -1, queue: map[int][]ir.Event{}}
}

func (s *serial) Event(ev ir.Event) error {
	switch ev.Kind {
	case ir.EvPartStart:
		if s.active == -1 && len(s.order) == 0 {
			s.active = ev.Index
			return s.ClientStream.Event(ev)
		}
		s.queue[ev.Index] = []ir.Event{ev}
		s.order = append(s.order, ev.Index)
		return nil
	case ir.EvDelta, ir.EvPartStop:
		if ev.Index != s.active {
			if _, ok := s.queue[ev.Index]; ok {
				s.queue[ev.Index] = append(s.queue[ev.Index], ev)
			}
			return nil
		}
		if err := s.ClientStream.Event(ev); err != nil {
			return err
		}
		if ev.Kind == ir.EvPartStop {
			s.active = -1
			return s.release()
		}
		return nil
	case ir.EvStop, ir.EvError:
		if err := s.drain(); err != nil {
			return err
		}
	}
	return s.ClientStream.Event(ev)
}

// release replays buffered parts until one is still open.
func (s *serial) release() error {
	for s.active == -1 && len(s.order) > 0 {
		idx := s.order[0]
		s.order = s.order[1:]
		evs := s.queue[idx]
		delete(s.queue, idx)
		s.active = idx
		for _, e := range evs {
			if err := s.ClientStream.Event(e); err != nil {
				return err
			}
			if e.Kind == ir.EvPartStop {
				s.active = -1
			}
		}
	}
	return nil
}

// drain flushes every buffered part, closing parts that never stopped.
func (s *serial) drain() error {
	if s.active != -1 {
		if err := s.ClientStream.Event(ir.Event{Kind: ir.EvPartStop, Index: s.active}); err != nil {
			return err
		}
		s.active = -1
	}
	if err := s.release(); err != nil {
		return err
	}
	if s.active != -1 {
		return s.drain()
	}
	return nil
}

func (s *serial) Finish(stop ir.StopReason) error {
	if err := s.drain(); err != nil {
		return err
	}
	return s.ClientStream.Finish(stop)
}
