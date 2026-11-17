// SPDX-License-Identifier: GPL-3.0-or-later

// Package dnstap takes what dnsinfo needs out of a dnstap message
// (https://dnstap.info, dnstap.proto): its kind, the DNS answer it
// carries, and when the answer was given.
//
// A dnstap message is a protocol buffer. Two messages and four fields of
// them are read here, by hand, and everything else is stepped over - the
// client's address among it, which dnsinfo has no business with.
//
//	message Dnstap  { optional Message message = 14; required Type type = 15; }
//	message Message { required Type type = 1;
//	                  optional uint64 response_time_sec = 12;
//	                  optional bytes  response_message  = 14; }
package dnstap

import (
	"errors"
)

// ContentType is what a dnstap stream calls itself in Frame Streams.
const ContentType = "protobuf:dnstap.Dnstap"

// ClientResponse is Message.Type CLIENT_RESPONSE: an answer the server
// gave one of its clients.
const ClientResponse = 6

// Message is the part of a dnstap message dnsinfo reads.
type Message struct {
	Type     int    // Message.Type; 0 when the frame carries no message
	Response []byte // the DNS answer as it went out; points into the frame
	Seconds  uint64 // when it went out (unix time); 0 when not said
}

var errShort = errors.New("a dnstap message cut short")

// Decode reads one frame.
func Decode(frame []byte) (Message, error) {
	var m Message
	err := fields(frame, func(number int, wire int, value uint64, bytes []byte) error {
		if number == 14 && wire == 2 { // Dnstap.message
			return fields(bytes, func(number int, wire int, value uint64, bytes []byte) error {
				switch {
				case number == 1 && wire == 0:
					m.Type = int(value)
				case number == 12 && wire == 0:
					m.Seconds = value
				case number == 14 && wire == 2:
					m.Response = bytes
				}
				return nil
			})
		}
		return nil
	})
	return m, err
}

// fields walks the fields of one protocol buffer message. Varints come
// in value, length-delimited fields in bytes; the fixed-width kinds are
// stepped over.
func fields(b []byte, each func(number int, wire int, value uint64, bytes []byte) error) error {
	for len(b) > 0 {
		key, n := varint(b)
		if n == 0 {
			return errShort
		}
		b = b[n:]
		number, wire := int(key>>3), int(key&7)
		switch wire {
		case 0:
			v, n := varint(b)
			if n == 0 {
				return errShort
			}
			b = b[n:]
			if err := each(number, wire, v, nil); err != nil {
				return err
			}
		case 1:
			if len(b) < 8 {
				return errShort
			}
			b = b[8:]
		case 2:
			size, n := varint(b)
			if n == 0 || uint64(len(b)-n) < size {
				return errShort
			}
			if err := each(number, wire, 0, b[n:n+int(size)]); err != nil {
				return err
			}
			b = b[n+int(size):]
		case 5:
			if len(b) < 4 {
				return errShort
			}
			b = b[4:]
		default:
			return errors.New("a dnstap message with a field kind that does not exist")
		}
	}
	return nil
}

// varint reads a base-128 number; n is 0 when there is none.
func varint(b []byte) (v uint64, n int) {
	for shift := uint(0); n < len(b) && n < 10; shift += 7 {
		c := b[n]
		n++
		v |= uint64(c&0x7f) << shift
		if c < 0x80 {
			return v, n
		}
	}
	return 0, 0
}
