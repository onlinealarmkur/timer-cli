package alert

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

type bellWriter func([]byte) (int, error)

func (w bellWriter) Write(data []byte) (int, error) { return w(data) }

func TestBell(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	if err := (Bell{Writer: &output, Count: 3}).Ring(); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "\a\a\a" {
		t.Fatalf("bells = %q", got)
	}
}

func TestBellDisabledAndBounded(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	if err := (Bell{Writer: &output, Disabled: true, Count: 3}).Ring(); err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 {
		t.Fatal("disabled bell wrote output")
	}
	err := (Bell{Writer: &output, Count: 4}).Ring()
	if err == nil || !strings.Contains(err.Error(), "between 1 and 3") {
		t.Fatalf("invalid count error = %v", err)
	}
}

func TestBellDefaultAndWriterFailures(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	if err := (Bell{Writer: &output}).Ring(); err != nil {
		t.Fatal(err)
	}
	if output.String() != "\a" {
		t.Fatalf("default bell output = %q", output.String())
	}

	writeErr := errors.New("output unavailable")
	tests := []struct {
		name string
		bell Bell
		want error
		text string
	}{
		{name: "nil writer", bell: Bell{}, text: "writer is required"},
		{name: "negative count", bell: Bell{Writer: &output, Count: -1}, text: "between 1 and 3"},
		{
			name: "writer error",
			bell: Bell{Writer: bellWriter(func([]byte) (int, error) { return 0, writeErr })},
			want: writeErr,
		},
		{
			name: "short write",
			bell: Bell{Writer: bellWriter(func([]byte) (int, error) { return 0, nil })},
			want: io.ErrShortWrite,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.bell.Ring()
			if err == nil || test.want != nil && !errors.Is(err, test.want) || test.text != "" && !strings.Contains(err.Error(), test.text) {
				t.Fatalf("Ring() error = %v, want error %v containing %q", err, test.want, test.text)
			}
		})
	}
}
