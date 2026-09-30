package mcp

import (
	"bufio"
	"bytes"
	"io"
	"strconv"
	"time"
)

// A text/event-stream parser following the WHATWG "event stream
// interpretation" rules (https://html.spec.whatwg.org/multipage/server-sent-events.html#event-stream-interpretation),
// shared by every client-side SSE consumer: a POST request's response stream
// and the long-lived notification streams (Legacy GET, Modern
// subscriptions/listen).
//
// Handled per the standard: CRLF, LF and lone-CR line endings; a leading
// UTF-8 BOM; comment lines (":..." — keep-alives) ignored; "field:value" with
// one optional leading space stripped from value; a line with no colon is a
// field with an empty value; multiple data lines joined with "\n"; an event
// is dispatched on a blank line, and one with an empty data buffer is not
// dispatched at all. The id field (ignored if it contains NUL) sets
// LastEventID when an event is dispatched — including an event whose data is
// empty, such as a 2025-11-25 "priming" event — and an all-digit retry field
// sets Retry, for Legacy stream resumption (2026-07-28 has no resumability).
//
// One deliberate leniency: a final event whose data arrived but whose
// terminating blank line did not (the server closed the stream straight
// after the last data line) is still dispatched at EOF, where the standard
// would discard it. Losing a complete JSON-RPC response to a missing trailing
// newline helps nobody, and the previous parser here accepted it too.

// sseMessage is one dispatched event.
type sseMessage struct {
	Event string // "message" unless the stream set an event type
	Data  []byte
}

type sseReader struct {
	r       *bufio.Reader
	started bool
	line    []byte
	idBuf   string

	// LastEventID is the id of the last dispatched event ("" if none).
	LastEventID string
	// Retry is the reconnection time the stream asked for (0 if unset).
	Retry time.Duration
}

func newSSEReader(r io.Reader) *sseReader {
	return &sseReader{r: bufio.NewReader(r)}
}

// readLine returns the next line without its terminator (CRLF, LF or CR).
// At EOF it returns any unterminated remainder with a nil error, then io.EOF.
func (s *sseReader) readLine() ([]byte, error) {
	s.line = s.line[:0]
	for {
		b, err := s.r.ReadByte()
		if err != nil {
			if err == io.EOF && len(s.line) > 0 {
				return s.line, nil
			}
			return nil, err
		}
		switch b {
		case '\n':
			return s.line, nil
		case '\r':
			if next, err := s.r.Peek(1); err == nil && next[0] == '\n' {
				s.r.ReadByte()
			}
			return s.line, nil
		default:
			s.line = append(s.line, b)
		}
	}
}

// next returns the next dispatched event, or io.EOF when the stream ends.
func (s *sseReader) next() (sseMessage, error) {
	var (
		event   string
		data    []byte
		hasData bool
	)
	for {
		line, err := s.readLine()
		if err != nil {
			if err == io.EOF && hasData {
				s.LastEventID = s.idBuf
				return finishSSEMessage(event, data), nil
			}
			return sseMessage{}, err
		}
		if !s.started {
			s.started = true
			line = bytes.TrimPrefix(line, []byte("\xEF\xBB\xBF"))
		}

		if len(line) == 0 {
			s.LastEventID = s.idBuf
			if hasData {
				return finishSSEMessage(event, data), nil
			}
			event = ""
			continue
		}
		if line[0] == ':' {
			continue
		}

		field, value := line, []byte(nil)
		if i := bytes.IndexByte(line, ':'); i >= 0 {
			field, value = line[:i], line[i+1:]
			value = bytes.TrimPrefix(value, []byte(" "))
		}
		switch string(field) {
		case "data":
			data = append(data, value...)
			data = append(data, '\n')
			hasData = true
		case "event":
			event = string(value)
		case "id":
			if bytes.IndexByte(value, 0) < 0 {
				s.idBuf = string(value)
			}
		case "retry":
			if ms, err := strconv.ParseUint(string(value), 10, 32); err == nil && isASCIIDigits(value) {
				s.Retry = time.Duration(ms) * time.Millisecond
			}
		}
	}
}

func finishSSEMessage(event string, data []byte) sseMessage {
	if event == "" {
		event = "message"
	}
	return sseMessage{Event: event, Data: bytes.TrimSuffix(data, []byte("\n"))}
}

func isASCIIDigits(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
