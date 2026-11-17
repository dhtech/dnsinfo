// SPDX-License-Identifier: GPL-3.0-or-later

package dnsmsg

import (
	"errors"
	"strings"
	"testing"
)

// Message builds a DNS message with one question.
func Message(flags uint16, name string, questions int) []byte {
	m := []byte{0x12, 0x34, byte(flags >> 8), byte(flags), 0, byte(questions), 0, 0, 0, 0, 0, 0}
	if questions == 0 {
		return m
	}
	if name != "." && name != "" {
		for _, label := range strings.Split(strings.TrimSuffix(name, "."), ".") {
			m = append(m, byte(len(label)))
			m = append(m, label...)
		}
	}
	return append(m, 0, 0, 1, 0, 1) // the root label, QTYPE A, QCLASS IN
}

const response = 0x8180 // QR, RD, RA

func TestAnswer(t *testing.T) {
	for _, c := range []struct {
		flags        uint16
		name         string
		want, result string
	}{
		{response, "www.example.org", "www.example.org", Answered},
		{response, "WWW.Example.ORG.", "www.example.org", Answered},
		{response | 3, "nothing.example.org", "nothing.example.org", NoName},
		{response | 2, "broken.example.org", "broken.example.org", Failed},
		{response | 5, "refused.example.org", "refused.example.org", Failed},
		{response, ".", ".", Answered},
		{response, "_sip._tcp.example.org", "_sip._tcp.example.org", Answered},
		{response, "bad name.example.org", Invalid, Answered},
		{response, "caf\xc3\xa9.example.org", Invalid, Answered},
	} {
		name, result, err := Answer(Message(c.flags, c.name, 1))
		if err != nil || name != c.want || result != c.result {
			t.Errorf("%q flags %#x: %q %q %v; want %q %q", c.name, c.flags, name, result, err, c.want, c.result)
		}
	}
}

func TestNotCounted(t *testing.T) {
	for name, msg := range map[string][]byte{
		"a question, not an answer": Message(0x0100, "www.example.org", 1),
		"a notify":                  Message(0x8000|4<<11, "example.org", 1),
		"an update":                 Message(0x8000|5<<11, "example.org", 1),
		"no question in it":         Message(response, "", 0),
	} {
		if _, _, err := Answer(msg); !errors.Is(err, ErrNotCounted) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestBrokenMessages(t *testing.T) {
	good := Message(response, "www.example.org", 1)
	for n := 0; n < len(good); n++ {
		if _, _, err := Answer(good[:n]); err == nil {
			t.Errorf("cut at %d of %d: no error", n, len(good))
		}
	}
	pointer := append(Message(response, "", 0)[:12], 0xc0, 0x0c, 0, 1, 0, 1)
	pointer[5] = 1
	if _, _, err := Answer(pointer); err == nil || errors.Is(err, ErrNotCounted) {
		t.Errorf("a compression pointer as the question name: %v", err)
	}
	long := Message(response, strings.Repeat("abcdefghi.", 30)+"org", 1)
	if _, _, err := Answer(long); err == nil {
		t.Error("a name of 300 bytes: no error")
	}
}
