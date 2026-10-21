package sse

import (
	"errors"
	"io"
	"strings"
	"testing"
)

type byteReader struct{ data string }

func (r *byteReader) Read(p []byte) (int, error) {
	if r.data == "" {
		return 0, io.EOF
	}

	p[0] = r.data[0]
	r.data = r.data[1:]
	return 1, nil
}

func TestWireFormats(t *testing.T) {
	for _, sep := range []string{"\n", "\r", "\r\n"} {
		wire := "\ufeff: heartbeat" + sep + sep + "data:" + sep + sep + "event: message" + sep + "data: one" + sep + "data: two" + sep + sep + "data: unfinished"
		for _, reader := range []io.Reader{strings.NewReader(wire), &byteReader{wire}} {
			d := New(reader, 1000)
			e, err := d.Next()
			if err != nil || e.Data != "one\ntwo" || e.Type != "message" {
				t.Fatalf("sep %q: %+v %v", sep, e, err)
			}

			if _, err = d.Next(); err != io.EOF {
				t.Fatal(err)
			}
		}
	}
}

func TestBoundedEvents(t *testing.T) {
	for _, wire := range []string{strings.Repeat("x", 100), "data: 12345678\ndata: 12345678\n\n"} {
		_, err := New(strings.NewReader(wire), 20).Next()
		if !errors.Is(err, ErrTooLarge) {
			t.Fatalf("got %v", err)
		}
	}
}

func FuzzDecoder(f *testing.F) {
	for _, s := range []string{"data: {}\n\n", "\ufeffdata: [DONE]\r\r", "data:\n\n", ": ping\r\n\r\n"} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		d := New(strings.NewReader(s), 1024)
		for i := 0; i <= len(s)+1; i++ {
			_, err := d.Next()
			if err != nil {
				return
			}
		}

		t.Fatal("decoder made no progress")
	})
}
