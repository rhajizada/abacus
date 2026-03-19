package sse_test

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/hajizar/abacus/internal/sse"
)

func TestReaderNextParsesStream(t *testing.T) {
	t.Parallel()

	reader := sse.NewReader(strings.NewReader(strings.Join([]string{
		": comment",
		"event: message",
		"id: 123",
		"data: first line",
		"data: second line",
		"",
		"data: trailing event",
		"",
	}, "\n")))

	first, err := reader.Next()
	if err != nil {
		t.Fatalf("first Next() error = %v, want nil", err)
	}
	if first.Event != "message" {
		t.Fatalf("first Event = %q, want %q", first.Event, "message")
	}
	if first.ID != "123" {
		t.Fatalf("first ID = %q, want %q", first.ID, "123")
	}
	if len(first.Data) != 2 || first.Data[0] != "first line" || first.Data[1] != "second line" {
		t.Fatalf("first Data = %#v, want two data lines", first.Data)
	}

	second, err := reader.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("second Next() error = %v, want EOF", err)
	}
	if second.Event != "" {
		t.Fatalf("second Event = %q, want empty", second.Event)
	}
	if second.ID != "123" {
		t.Fatalf("second ID = %q, want %q", second.ID, "123")
	}
	if len(second.Data) != 1 || second.Data[0] != "trailing event" {
		t.Fatalf("second Data = %#v, want trailing event", second.Data)
	}

	_, err = reader.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("third Next() error = %v, want EOF", err)
	}
}

func TestReaderNextFlushesBufferedEventOnEOF(t *testing.T) {
	t.Parallel()

	reader := sse.NewReader(strings.NewReader("event: done\ndata: payload"))

	event, err := reader.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Next() error = %v, want EOF", err)
	}
	if event.Event != "done" {
		t.Fatalf("Event = %q, want %q", event.Event, "done")
	}
	if len(event.Data) != 1 || event.Data[0] != "payload" {
		t.Fatalf("Data = %#v, want payload", event.Data)
	}

	_, err = reader.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("second Next() error = %v, want EOF", err)
	}
}

func TestReaderNextSkipsLeadingBlankEvents(t *testing.T) {
	t.Parallel()

	reader := sse.NewReader(strings.NewReader("\n\n:data ignored\n\ndata: value\n\n"))

	event, err := reader.Next()
	if err != nil {
		t.Fatalf("Next() error = %v, want nil", err)
	}
	if event.Event != "" || event.ID != "" {
		t.Fatalf("event metadata = %#v, want empty event and id", event)
	}
	if len(event.Data) != 1 || event.Data[0] != "value" {
		t.Fatalf("Data = %#v, want value", event.Data)
	}
}
