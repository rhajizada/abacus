package sse_test

import (
	"io"
	"strings"
	"testing"

	"github.com/hajizar/abacus/internal/sse"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type expectedRead struct {
	event sse.Event
	err   error
}

func TestReaderNext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		reads []expectedRead
	}{
		{
			name: "parses stream",
			input: strings.Join([]string{
				": comment",
				"event: message",
				"id: 123",
				"data: first line",
				"data: second line",
				"",
				"data: trailing event",
				"",
			}, "\n"),
			reads: []expectedRead{
				{event: sse.Event{Event: "message", ID: "123", Data: []string{"first line", "second line"}}},
				{event: sse.Event{ID: "123", Data: []string{"trailing event"}}, err: io.EOF},
				{event: sse.Event{ID: "123"}, err: io.EOF},
			},
		},
		{
			name:  "flushes buffered event on eof",
			input: "event: done\ndata: payload",
			reads: []expectedRead{
				{event: sse.Event{Event: "done", Data: []string{"payload"}}, err: io.EOF},
				{event: sse.Event{}, err: io.EOF},
			},
		},
		{
			name:  "skips leading blank events",
			input: "\n\n:data ignored\n\ndata: value\n\n",
			reads: []expectedRead{
				{event: sse.Event{Data: []string{"value"}}},
				{event: sse.Event{}, err: io.EOF},
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			reader := sse.NewReader(strings.NewReader(testCase.input))
			for _, want := range testCase.reads {
				got, err := reader.Next()
				assert.Equal(t, want.event, got)
				if want.err == nil {
					require.NoError(t, err)
					continue
				}

				require.ErrorIs(t, err, want.err)
			}
		})
	}
}
