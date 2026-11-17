// SPDX-License-Identifier: GPL-3.0-or-later

// Package fstrm reads a Frame Streams connection, the transport dnstap
// messages travel in (https://github.com/farsightsec/fstrm).
//
// On the wire everything is a frame. A data frame is a 32-bit big-endian
// length, not zero, and that many bytes. A length of zero announces a
// control frame: another 32-bit length, then a 32-bit type and fields
// (each: 32-bit field type, 32-bit length, payload).
//
// The writer - the name server - connects to us and the two shake hands:
//
//	writer: READY   (the content types it can send)
//	reader: ACCEPT  (the one we take)
//	writer: START   (the one it sends)
//	writer: data frames ...
//	writer: STOP
//	reader: FINISH
package fstrm

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Control frame types and the one field type there is.
const (
	controlAccept = 1
	controlStart  = 2
	controlStop   = 3
	controlReady  = 4
	controlFinish = 5

	fieldContentType = 1
)

const (
	maxControlFrame = 512
	// MaxFrame is the largest data frame taken. A DNS message is at most
	// 64 KiB; a dnstap message is that and a little.
	MaxFrame = 256 * 1024
)

// ErrStopped says the writer ended the stream in good order.
var ErrStopped = errors.New("the writer stopped the stream")

// Reader hands out the data frames of one connection.
type Reader struct {
	r    *bufio.Reader
	w    io.Writer
	done bool
}

// Handshake does the reader's part of the handshake on rw and returns a
// Reader for the data frames. The writer must offer contentType.
func Handshake(rw io.ReadWriter, contentType string) (*Reader, error) {
	rd := &Reader{r: bufio.NewReaderSize(rw, 64*1024), w: rw}
	kind, types, err := rd.control()
	if err != nil {
		return nil, fmt.Errorf("waiting for READY: %w", err)
	}
	if kind != controlReady {
		return nil, fmt.Errorf("expected READY, got control frame %d", kind)
	}
	if !contains(types, contentType) {
		return nil, fmt.Errorf("the writer offers %q, not %q", types, contentType)
	}
	if err := writeControl(rw, controlAccept, contentType); err != nil {
		return nil, fmt.Errorf("sending ACCEPT: %w", err)
	}
	kind, types, err = rd.control()
	if err != nil {
		return nil, fmt.Errorf("waiting for START: %w", err)
	}
	if kind != controlStart {
		return nil, fmt.Errorf("expected START, got control frame %d", kind)
	}
	if len(types) > 0 && !contains(types, contentType) {
		return nil, fmt.Errorf("the writer starts %q, not %q", types, contentType)
	}
	return rd, nil
}

// Frame returns the next data frame. The slice is only good until the
// next call. After the writer's STOP it answers FINISH and returns
// ErrStopped.
func (rd *Reader) Frame(buf []byte) ([]byte, error) {
	if rd.done {
		return nil, ErrStopped
	}
	for {
		n, err := rd.length()
		if err != nil {
			return nil, err
		}
		if n == 0 {
			kind, _, err := rd.controlBody()
			if err != nil {
				return nil, err
			}
			if kind == controlStop {
				rd.done = true
				// the writer may be gone already: FINISH is a courtesy
				_ = writeControl(rd.w, controlFinish, "")
				return nil, ErrStopped
			}
			continue // a control frame we have no use for
		}
		if n > MaxFrame {
			return nil, fmt.Errorf("a frame of %d bytes (the limit is %d)", n, MaxFrame)
		}
		if cap(buf) < int(n) {
			buf = make([]byte, n)
		}
		buf = buf[:n]
		if _, err := io.ReadFull(rd.r, buf); err != nil {
			return nil, err
		}
		return buf, nil
	}
}

func (rd *Reader) length() (uint32, error) {
	var b [4]byte
	if _, err := io.ReadFull(rd.r, b[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(b[:]), nil
}

// control reads a control frame from its escape on.
func (rd *Reader) control() (uint32, []string, error) {
	n, err := rd.length()
	if err != nil {
		return 0, nil, err
	}
	if n != 0 {
		return 0, nil, errors.New("a data frame where a control frame was expected")
	}
	return rd.controlBody()
}

// controlBody reads a control frame after its escape: its type and the
// content types it names.
func (rd *Reader) controlBody() (uint32, []string, error) {
	n, err := rd.length()
	if err != nil {
		return 0, nil, err
	}
	if n < 4 || n > maxControlFrame {
		return 0, nil, fmt.Errorf("a control frame of %d bytes", n)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(rd.r, body); err != nil {
		return 0, nil, err
	}
	kind := binary.BigEndian.Uint32(body)
	var types []string
	for rest := body[4:]; len(rest) > 0; {
		if len(rest) < 8 {
			return 0, nil, errors.New("a control frame field cut short")
		}
		field, size := binary.BigEndian.Uint32(rest), binary.BigEndian.Uint32(rest[4:])
		rest = rest[8:]
		if uint32(len(rest)) < size {
			return 0, nil, errors.New("a control frame field cut short")
		}
		if field == fieldContentType {
			types = append(types, string(rest[:size]))
		}
		rest = rest[size:]
	}
	return kind, types, nil
}

func writeControl(w io.Writer, kind uint32, contentType string) error {
	size := 4
	if contentType != "" {
		size += 8 + len(contentType)
	}
	b := make([]byte, 8, 8+size)
	binary.BigEndian.PutUint32(b[4:], uint32(size))
	b = binary.BigEndian.AppendUint32(b, kind)
	if contentType != "" {
		b = binary.BigEndian.AppendUint32(b, fieldContentType)
		b = binary.BigEndian.AppendUint32(b, uint32(len(contentType)))
		b = append(b, contentType...)
	}
	_, err := w.Write(b)
	return err
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
