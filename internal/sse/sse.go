package sse

import (
	"bufio"
	"io"
	"strings"
)

type Event struct {
	Event string
	Data  []string
	ID    string
}

type Reader struct {
	scanner *bufio.Scanner
	buffer  []string
	eventID string
	name    string
}

const (
	initialBufferSize = 64 * 1024
	maxBufferSize     = 2 * 1024 * 1024
)

func NewReader(r io.Reader) *Reader {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, initialBufferSize), maxBufferSize)
	return &Reader{scanner: scanner}
}

func (r *Reader) Next() (Event, error) {
	for r.scanner.Scan() {
		line := strings.TrimRight(r.scanner.Text(), "\r")
		if line == "" {
			if len(r.buffer) == 0 && r.name == "" && r.eventID == "" {
				continue
			}
			event := Event{Event: r.name, Data: append([]string(nil), r.buffer...), ID: r.eventID}
			r.buffer = r.buffer[:0]
			r.name = ""
			return event, nil
		}
		if strings.HasPrefix(line, ":") {
			continue
		}

		field, value, found := strings.Cut(line, ":")
		if !found {
			field = line
			value = ""
		} else {
			value = strings.TrimPrefix(value, " ")
		}

		switch field {
		case "event":
			r.name = value
		case "data":
			r.buffer = append(r.buffer, value)
		case "id":
			r.eventID = value
		}
	}

	if err := r.scanner.Err(); err != nil {
		return Event{}, err
	}
	if len(r.buffer) > 0 || r.name != "" || r.eventID != "" {
		event := Event{Event: r.name, Data: append([]string(nil), r.buffer...), ID: r.eventID}
		r.buffer = nil
		r.name = ""
		return event, io.EOF
	}

	return Event{}, io.EOF
}
