// SPDX-License-Identifier: GPL-3.0-or-later

// Package dnsmsg reads two things out of a DNS answer as it is on the
// wire (RFC 1035): the name that was asked for, and how it went.
package dnsmsg

import (
	"errors"
)

// How an answer went.
const (
	Answered = "answered" // NOERROR - an answer, or "the name exists, not that kind of record"
	NoName   = "noname"   // NXDOMAIN - no such name
	Failed   = "failed"   // everything else: SERVFAIL, REFUSED, FORMERR ...
)

// Invalid stands for a name that is no host name: bytes outside letters,
// digits, "-" and "_".
const Invalid = "(invalid)"

// ErrNotCounted says the message is not an answer to an ordinary
// question (a notify, an update, a question itself, or no question in it).
var ErrNotCounted = errors.New("not an answer to a question")

// Answer returns the name asked for - in small letters, without the dot
// at the end, "." for the root - and how the answer went.
func Answer(msg []byte) (name string, result string, err error) {
	if len(msg) < 12 {
		return "", "", errors.New("a DNS message shorter than its header")
	}
	flags := uint16(msg[2])<<8 | uint16(msg[3])
	if flags&0x8000 == 0 || (flags>>11)&0xf != 0 { // not a response, or not QUERY
		return "", "", ErrNotCounted
	}
	if uint16(msg[4])<<8|uint16(msg[5]) == 0 { // QDCOUNT
		return "", "", ErrNotCounted
	}
	switch flags & 0xf {
	case 0:
		result = Answered
	case 3:
		result = NoName
	default:
		result = Failed
	}
	name, err = question(msg[12:])
	return name, result, err
}

// question reads the first name of the question section.
func question(b []byte) (string, error) {
	out := make([]byte, 0, 64)
	valid := true
	for {
		if len(b) == 0 {
			return "", errors.New("a question name cut short")
		}
		n := int(b[0])
		b = b[1:]
		if n == 0 {
			break
		}
		if n > 63 { // a compression pointer has no place in a question
			return "", errors.New("a question name that is no plain name")
		}
		if len(b) < n {
			return "", errors.New("a question name cut short")
		}
		if len(out) > 0 {
			out = append(out, '.')
		}
		for _, c := range b[:n] {
			switch {
			case c >= 'A' && c <= 'Z':
				c += 'a' - 'A'
			case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_':
			default:
				valid = false
			}
			out = append(out, c)
		}
		b = b[n:]
		if len(out) > 253 {
			return "", errors.New("a question name longer than a name may be")
		}
	}
	if len(b) < 4 { // QTYPE and QCLASS must follow
		return "", errors.New("a question cut short")
	}
	switch {
	case !valid:
		return Invalid, nil
	case len(out) == 0:
		return ".", nil
	}
	return string(out), nil
}
