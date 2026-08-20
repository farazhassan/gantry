package vercelai

import (
	"io"
	"sync"

	"github.com/farazhassan/gantry"
)

// Sink adapts a Mapper to a gantry.EventSink that writes UI Message Stream
// SSE frames to an io.Writer. mu serializes Map plus the resulting write so
// concurrent-origin events (see gantry.EventSink's doc on
// EventToolResultLive) never touch Mapper's internal state or the
// underlying io.Writer concurrently. An optional flush callback (SetFlusher)
// is invoked once per incoming Gantry event, after all of that event's
// chunks are written, so an HTTP server streams to the client promptly.
type Sink struct {
	mu     sync.Mutex
	w      io.Writer
	mapper *Mapper
	flush  func()
	closed bool // terminal "[DONE]" line written?
}

// NewSink returns a Sink writing to w, building a single assistant message
// identified by messageID.
func NewSink(w io.Writer, messageID string) *Sink {
	return &Sink{w: w, mapper: NewMapper(messageID)}
}

// SetFlusher registers a callback invoked once per Gantry event, after that
// event's chunks are written (e.g. http.Flusher.Flush). A nil callback
// disables flushing.
func (s *Sink) SetFlusher(flush func()) { s.flush = flush }

// Sink returns a gantry.EventSink that maps each Gantry event to UI Message
// Stream chunks and writes them as SSE frames, flushing after each Gantry
// event. A write error aborts the run (it propagates out of RunStream).
func (s *Sink) Sink() gantry.EventSink {
	return func(ev gantry.Event) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, c := range s.mapper.Map(ev) {
			if err := WriteSSE(s.w, c); err != nil {
				return err
			}
		}
		if s.flush != nil {
			s.flush()
		}
		return nil
	}
}

// Heartbeat writes a bare SSE comment line ("keep-alive ping") and flushes
// it, guarded by the same mutex as Sink/EmitError so it interleaves safely
// with concurrent event writes. A line starting with ':' is a comment per
// the SSE spec; the AI SDK's fetch-based reader only looks at "data:"
// lines, so this is invisible to clients. The handler calls this on an
// idle ticker so a slow tool call or a silently-thinking model doesn't
// leave the connection with no bytes long enough for a reverse proxy or
// load balancer's read-timeout to kill it.
func (s *Sink) Heartbeat() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := io.WriteString(s.w, ": ping\n\n"); err != nil {
		return err
	}
	if s.flush != nil {
		s.flush()
	}
	return nil
}

// EmitError writes an "error" chunk. The HTTP handler calls this when
// RunFromStream/ResumeStream returns an error after the SSE response has
// already begun, so the client learns the run failed (the status code is
// already committed).
//
// A "start" chunk is emitted first if it hasn't been already (the run can
// fail before any Gantry event arrives, e.g. a cancelled context), and any
// open text/reasoning block and open step are closed, so clients always
// see a well-formed start -> ... -> error stream. It does not write the
// terminal "[DONE]" line -- see Close, which the handler always calls
// last regardless of outcome.
func (s *Sink) EmitError(err error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	frames := s.mapper.startFrame()
	frames = append(frames, s.mapper.closeText()...)
	frames = append(frames, s.mapper.closeReasoning()...)
	frames = append(frames, s.mapper.closeStep()...)
	frames = append(frames, newErrorChunk(err.Error()))
	for _, c := range frames {
		if werr := WriteSSE(s.w, c); werr != nil {
			return werr
		}
	}
	if s.flush != nil {
		s.flush()
	}
	return nil
}

// Close writes the terminal "data: [DONE]\n\n" line the AI SDK client uses
// to detect stream end -- verified against the SDK's own
// JsonToSseTransformStream, which does this unconditionally on flush
// regardless of how the stream ended. Idempotent (writes it at most once)
// and safe to call even if the stream already errored or panicked; the
// handler defers this unconditionally right after creating the Sink.
func (s *Sink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if err := writeDone(s.w); err != nil {
		return err
	}
	if s.flush != nil {
		s.flush()
	}
	return nil
}
