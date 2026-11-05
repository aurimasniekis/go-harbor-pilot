package wire

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrLineTooLong reports an SSE line exceeding the configured bound.
var ErrLineTooLong = errors.New("sse: line too long")

// SSEEvent is one server-sent event.
type SSEEvent struct {
	Event string
	Data  []byte
	ID    string
}

// SSEReader reads server-sent events with bounded memory: each data payload
// is limited to MaxEventBytes.
type SSEReader struct {
	r             *bufio.Reader
	MaxEventBytes int
}

// NewSSEReader wraps r. maxEvent bounds a single event (default 8 MiB).
func NewSSEReader(r io.Reader, maxEvent int) *SSEReader {
	if maxEvent <= 0 {
		maxEvent = 8 << 20
	}
	return &SSEReader{r: bufio.NewReaderSize(r, 64<<10), MaxEventBytes: maxEvent}
}

// Next returns the next event. Comment lines are skipped. At end of stream it
// returns io.EOF; a final event without a trailing blank line is delivered.
func (s *SSEReader) Next() (*SSEEvent, error) {
	ev := &SSEEvent{}
	var data bytes.Buffer
	hasData := false
	for {
		line, err := s.readLine()
		if err != nil {
			if errors.Is(err, io.EOF) && (hasData || ev.Event != "") {
				ev.Data = data.Bytes()
				return ev, nil
			}
			return nil, err
		}
		if len(line) == 0 {
			if hasData || ev.Event != "" {
				ev.Data = data.Bytes()
				return ev, nil
			}
			continue
		}
		if line[0] == ':' {
			continue
		}
		field, value, _ := bytes.Cut(line, []byte(":"))
		if len(value) > 0 && value[0] == ' ' {
			value = value[1:]
		}
		switch string(field) {
		case "event":
			ev.Event = string(value)
		case "data":
			if hasData {
				data.WriteByte('\n')
			}
			if data.Len()+len(value) > s.MaxEventBytes {
				return nil, ErrLineTooLong
			}
			data.Write(value)
			hasData = true
		case "id":
			ev.ID = string(value)
		}
	}
}

func (s *SSEReader) readLine() ([]byte, error) {
	var out []byte
	for {
		chunk, isPrefix, err := s.r.ReadLine()
		if err != nil {
			if len(out) > 0 && errors.Is(err, io.EOF) {
				return out, nil
			}
			return nil, err
		}
		if len(out)+len(chunk) > s.MaxEventBytes {
			return nil, ErrLineTooLong
		}
		out = append(out, chunk...)
		if !isPrefix {
			return bytes.TrimSuffix(out, []byte("\r")), nil
		}
	}
}

// SSEWriter writes server-sent events and flushes after each one.
type SSEWriter struct {
	w  http.ResponseWriter
	rc *http.ResponseController
	// Bytes counts bytes written to the client.
	Bytes int64
	// Started is true once any byte has been written.
	Started bool
}

// NewSSEWriter prepares w for an event stream.
func NewSSEWriter(w http.ResponseWriter) *SSEWriter {
	return &SSEWriter{w: w, rc: http.NewResponseController(w)}
}

// Header writes SSE response headers (once).
func (s *SSEWriter) Header(status int) {
	h := s.w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	h.Del("Content-Length")
	s.w.WriteHeader(status)
}

// Write writes one event. Empty event omits the event: line.
func (s *SSEWriter) Write(event string, data []byte) error {
	var b strings.Builder
	if event != "" {
		b.WriteString("event: ")
		b.WriteString(event)
		b.WriteByte('\n')
	}
	for _, line := range bytes.Split(data, []byte("\n")) {
		b.WriteString("data: ")
		b.Write(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	return s.raw(b.String())
}

// Comment writes an SSE comment (keepalive bytes that clients ignore).
func (s *SSEWriter) Comment(text string) error { return s.raw(": " + text + "\n\n") }

// Raw writes preformatted bytes (used by passthrough).
func (s *SSEWriter) Raw(b []byte) error { return s.raw(string(b)) }

func (s *SSEWriter) raw(str string) error {
	n, err := io.WriteString(s.w, str)
	s.Bytes += int64(n)
	if n > 0 {
		s.Started = true
	}
	if err != nil {
		return err
	}
	return s.rc.Flush()
}

// SetWriteDeadline bounds writes to a slow client (zero clears it).
func (s *SSEWriter) SetWriteDeadline(t time.Time) error { return s.rc.SetWriteDeadline(t) }
