// SPDX-License-Identifier: GPL-3.0-or-later

// Package dnsmsg takes two things out of a DNS answer: the name that
// was asked for, and how it went. The message itself is read by the Go
// project's parser (golang.org/x/net/dns/dnsmessage).
package dnsmsg

import (
	"errors"

	"golang.org/x/net/dns/dnsmessage"
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
	var p dnsmessage.Parser
	h, err := p.Start(msg)
	if err != nil {
		return "", "", err
	}
	if !h.Response || h.OpCode != 0 { // not a response, or not QUERY
		return "", "", ErrNotCounted
	}
	q, err := p.Question()
	if errors.Is(err, dnsmessage.ErrSectionDone) {
		return "", "", ErrNotCounted
	}
	if err != nil {
		return "", "", err
	}
	switch h.RCode {
	case dnsmessage.RCodeSuccess:
		result = Answered
	case dnsmessage.RCodeNameError:
		result = NoName
	default:
		result = Failed
	}
	return hostName(q.Name.Data[:q.Name.Length]), result, nil
}

// hostName is a name as the parser gives it ("WWW.Example.org.") the
// way it is counted.
func hostName(b []byte) string {
	if len(b) > 0 && b[len(b)-1] == '.' {
		b = b[:len(b)-1]
	}
	if len(b) == 0 {
		return "."
	}
	out := make([]byte, len(b))
	for i, c := range b {
		switch {
		case c >= 'A' && c <= 'Z':
			c += 'a' - 'A'
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
		default:
			return Invalid
		}
		out[i] = c
	}
	return string(out)
}
