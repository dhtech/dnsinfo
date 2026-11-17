// SPDX-License-Identifier: GPL-3.0-or-later

package fstrm

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
)

const ct = "protobuf:dnstap.Dnstap"

// control builds a control frame as a writer sends it.
func control(kind uint32, types ...string) []byte {
	body := binary.BigEndian.AppendUint32(nil, kind)
	for _, t := range types {
		body = binary.BigEndian.AppendUint32(body, fieldContentType)
		body = binary.BigEndian.AppendUint32(body, uint32(len(t)))
		body = append(body, t...)
	}
	out := make([]byte, 4, 8+len(body))
	out = binary.BigEndian.AppendUint32(out, uint32(len(body)))
	return append(out, body...)
}

func data(payload string) []byte {
	return append(binary.BigEndian.AppendUint32(nil, uint32(len(payload))), payload...)
}

// writer plays the name server on one end of a pipe.
func writer(t *testing.T, conn net.Conn, offer []string, frames ...string) {
	t.Helper()
	defer conn.Close()
	conn.Write(control(controlReady, offer...))
	want := control(controlAccept, ct)
	got := make([]byte, len(want))
	if _, err := io.ReadFull(conn, got); err != nil {
		return // the reader refused: its test looks at that
	}
	if !bytes.Equal(got, want) {
		t.Errorf("the reader answered % x, want ACCEPT % x", got, want)
	}
	conn.Write(control(controlStart, ct))
	for _, f := range frames {
		conn.Write(data(f))
	}
	conn.Write(control(controlStop))
	want = control(controlFinish)
	got = make([]byte, len(want))
	if _, err := io.ReadFull(conn, got); err != nil || !bytes.Equal(got, want) {
		t.Errorf("after STOP the reader sent % x (%v), want FINISH", got, err)
	}
}

func TestHandshakeAndFrames(t *testing.T) {
	a, b := net.Pipe()
	go writer(t, b, []string{ct}, "first", "second frame")
	rd, err := Handshake(a, ct)
	if err != nil {
		t.Fatal(err)
	}
	var buf []byte
	for _, want := range []string{"first", "second frame"} {
		frame, err := rd.Frame(buf)
		if err != nil || string(frame) != want {
			t.Fatalf("frame %q, %v; want %q", frame, err, want)
		}
		buf = frame
	}
	if _, err := rd.Frame(buf); !errors.Is(err, ErrStopped) {
		t.Fatalf("after the last frame: %v, want ErrStopped", err)
	}
	if _, err := rd.Frame(buf); !errors.Is(err, ErrStopped) {
		t.Fatalf("and again: %v, want ErrStopped", err)
	}
}

func TestAWriterOfferingSeveralTypes(t *testing.T) {
	a, b := net.Pipe()
	go writer(t, b, []string{"something:else", ct}, "x")
	rd, err := Handshake(a, ct)
	if err != nil {
		t.Fatal(err)
	}
	if frame, err := rd.Frame(nil); err != nil || string(frame) != "x" {
		t.Fatalf("frame %q, %v", frame, err)
	}
}

func TestAWriterOfferingSomethingElseIsRefused(t *testing.T) {
	a, b := net.Pipe()
	go writer(t, b, []string{"something:else"})
	if _, err := Handshake(a, ct); err == nil {
		t.Fatal("a stream of another content type was accepted")
	}
	a.Close()
}

func TestBrokenStreams(t *testing.T) {
	for name, stream := range map[string][]byte{
		"a data frame first":   data("hello"),
		"START without READY":  control(controlStart, ct),
		"a huge control frame": {0, 0, 0, 0, 0, 1, 0, 0},
		"cut short":            control(controlReady, ct)[:9],
	} {
		a, b := net.Pipe()
		go func() { b.Write(stream); b.Close() }()
		if _, err := Handshake(a, ct); err == nil {
			t.Errorf("%s: accepted", name)
		}
		a.Close()
	}
}

func TestAFrameTooLargeEndsTheStream(t *testing.T) {
	a, b := net.Pipe()
	go func() {
		b.Write(control(controlReady, ct))
		io.ReadFull(b, make([]byte, len(control(controlAccept, ct))))
		b.Write(control(controlStart, ct))
		b.Write(binary.BigEndian.AppendUint32(nil, MaxFrame+1))
		b.Close()
	}()
	rd, err := Handshake(a, ct)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rd.Frame(nil); err == nil || errors.Is(err, ErrStopped) {
		t.Fatalf("a frame over the limit: %v", err)
	}
}
