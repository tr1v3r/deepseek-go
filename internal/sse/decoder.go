// Package sse decodes bounded SSE events without reconnecting or interpreting JSON.
package sse

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
)

var ErrTooLarge = errors.New("SSE event exceeds configured limit")

type Event struct{ Type, Data string }

type Decoder struct {
	scanner *bufio.Scanner
	first   bool
	limit   int
}

func New(r io.Reader, limit int) *Decoder {
	if limit <= 0 {
		limit = 4 << 20
	}

	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, min(limit+2, 4096)), limit+2)
	// CR, LF, CRLF are all separators. Skip the LF of CRLF even when reads split it.
	afterCR := false
	s.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		skipped := 0
		if afterCR && len(data) > 0 {
			afterCR = false
			if data[0] == '\n' {
				skipped = 1
				data = data[1:]
			}
		}

		if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
			afterCR = data[i] == '\r'
			return skipped + i + 1, data[:i], nil
		}

		if len(data) > limit {
			return 0, nil, ErrTooLarge
		}

		if atEOF && len(data) > 0 {
			return skipped + len(data), data, nil
		}

		return skipped, nil, nil
	})
	return &Decoder{scanner: s, first: true, limit: limit}
}

// Next ignores comment-only and empty-data events. EOF never dispatches an
// unfinished event; callers decide whether EOF is a clean protocol termination.
func (d *Decoder) Next() (Event, error) {
	var event Event
	var data strings.Builder
	size := 0
	for d.scanner.Scan() {
		line := d.scanner.Text()
		if d.first {
			line = strings.TrimPrefix(line, "\ufeff")
			d.first = false
		}

		size += len(line)
		if size > d.limit {
			return Event{}, ErrTooLarge
		}

		if line == "" {
			if data.Len() > 0 {
				event.Data = strings.TrimSuffix(data.String(), "\n")
				if event.Data != "" {
					return event, nil
				}
			}

			event = Event{}
			data.Reset()
			size = 0
			continue
		}

		if strings.HasPrefix(line, ":") {
			continue
		}

		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "data":
			data.WriteString(value)
			data.WriteByte('\n')
			size++
			if size > d.limit {
				return Event{}, ErrTooLarge
			}
		case "event":
			event.Type = value
		}
	}

	if err := d.scanner.Err(); err != nil {
		return Event{}, err
	}

	return Event{}, io.EOF
}
