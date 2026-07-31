package agui

import (
	"io"
	"sync"

	"github.com/farazhassan/gantry"
)

// Sink adapts a Mapper to a gantry.EventSink that writes AG-UI SSE frames to
// an io.Writer. One Sink wraps one Mapper for a single AG-UI thread, but —
// per Mapper's own doc comment — that one Mapper can legitimately see events
// from several Gantry runs once sub-agent passthrough is active: with
// components/subagent's WithEventPassthrough enabled, more than one Gantry
// run (the parent's own, plus any nested sub-agent runs invoked in parallel
// via components/tool's parallel dispatch) can call the EventSink returned by
// Sink from different goroutines at once. mu serializes Map plus the
// resulting write so Mapper's internal state and the underlying io.Writer are
// never touched concurrently. An optional flush callback (set via SetFlusher)
// is invoked once per incoming Gantry event, after all of that event's AG-UI
// frames are written, so an HTTP server streams to the client promptly.
type Sink struct {
	mu     sync.Mutex
	w      io.Writer
	mapper *Mapper
	flush  func()
}

// NewSink returns a Sink writing to w for the run identified by threadID/runID.
func NewSink(w io.Writer, threadID, runID string) *Sink {
	return &Sink{w: w, mapper: NewMapper(threadID, runID)}
}

// SetFlusher registers a callback invoked once per Gantry event, after that
// event's frames are written (e.g. http.Flusher.Flush). A nil callback disables
// flushing.
func (s *Sink) SetFlusher(flush func()) { s.flush = flush }

// Sink returns a gantry.EventSink that maps each Gantry event to AG-UI events
// and writes them as SSE frames, flushing after each Gantry event. A write
// error aborts the run (it propagates out of RunStream).
func (s *Sink) Sink() gantry.EventSink {
	return func(ev gantry.Event) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, ae := range s.mapper.Map(ev) {
			if err := WriteSSE(s.w, ae); err != nil {
				return err
			}
		}
		if s.flush != nil {
			s.flush()
		}
		return nil
	}
}

// EmitError writes a RUN_ERROR frame. The HTTP handler calls this when
// RunFromStream returns an error after the SSE response has already begun, so
// the client learns the run failed (the status code is already committed).
//
// RUN_STARTED is emitted first if it hasn't been already (the run can fail
// before any Gantry event arrives, e.g. a cancelled context), and any open text
// message is closed, so clients always see a well-formed RUN_STARTED → … →
// RUN_ERROR stream.
func (s *Sink) EmitError(err error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	frames := s.mapper.startFrame()
	frames = append(frames, s.mapper.closeAllText()...)
	frames = append(frames, newRunError(err.Error()))
	for _, ae := range frames {
		if werr := WriteSSE(s.w, ae); werr != nil {
			return werr
		}
	}
	if s.flush != nil {
		s.flush()
	}
	return nil
}
