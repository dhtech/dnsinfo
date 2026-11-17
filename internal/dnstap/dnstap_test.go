// SPDX-License-Identifier: GPL-3.0-or-later

package dnstap

import (
	"bytes"
	"testing"
)

// The protocol buffer encoding, as far as the tests need it.
func uvarint(v uint64) []byte {
	var b []byte
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}
func vfield(number int, v uint64) []byte {
	return append(uvarint(uint64(number)<<3), uvarint(v)...)
}
func bfield(number int, b []byte) []byte {
	return append(append(uvarint(uint64(number)<<3|2), uvarint(uint64(len(b)))...), b...)
}
func f32(number int) []byte { return append(uvarint(uint64(number)<<3|5), 1, 2, 3, 4) }

// Frame builds a dnstap frame as a name server sends it for an answer to
// a client: with the client's address and everything else around what
// is read.
func Frame(kind int, seconds uint64, response []byte) []byte {
	var m []byte
	m = append(m, vfield(1, uint64(kind))...)         // type
	m = append(m, vfield(2, 1)...)                    // socket_family INET
	m = append(m, vfield(3, 1)...)                    // socket_protocol UDP
	m = append(m, bfield(4, []byte{192, 0, 2, 7})...) // query_address
	m = append(m, vfield(6, 53001)...)                // query_port
	m = append(m, vfield(8, seconds)...)              // query_time_sec
	m = append(m, f32(9)...)                          // query_time_nsec
	if seconds != 0 {
		m = append(m, vfield(12, seconds)...) // response_time_sec
		m = append(m, f32(13)...)             // response_time_nsec
	}
	if response != nil {
		m = append(m, bfield(14, response)...) // response_message
	}
	var d []byte
	d = append(d, bfield(1, []byte("resolver.example.org"))...) // identity
	d = append(d, bfield(2, []byte("a name server"))...)        // version
	d = append(d, bfield(14, m)...)                             // message
	d = append(d, vfield(15, 1)...)                             // type MESSAGE
	return d
}

func TestDecode(t *testing.T) {
	answer := []byte("the dns answer")
	m, err := Decode(Frame(ClientResponse, 1767322800, answer))
	if err != nil {
		t.Fatal(err)
	}
	if m.Type != ClientResponse || m.Seconds != 1767322800 || !bytes.Equal(m.Response, answer) {
		t.Fatalf("got %+v", m)
	}
}

func TestOtherKindsAndMissingParts(t *testing.T) {
	m, err := Decode(Frame(5, 0, nil)) // CLIENT_QUERY, no response
	if err != nil || m.Type != 5 || m.Response != nil || m.Seconds != 0 {
		t.Fatalf("a query: %+v, %v", m, err)
	}
	m, err = Decode(vfield(15, 1)) // a frame without a message
	if err != nil || m.Type != 0 {
		t.Fatalf("no message: %+v, %v", m, err)
	}
	if m, err = Decode(nil); err != nil || m.Type != 0 {
		t.Fatalf("an empty frame: %+v, %v", m, err)
	}
}

func TestCutShortIsAnErrorNotAPanic(t *testing.T) {
	full := Frame(ClientResponse, 1767322800, []byte("the dns answer"))
	for n := 1; n < len(full); n++ {
		// most cuts are errors; a cut between two fields is a shorter
		// message. Neither may panic or read past the end.
		m, err := Decode(full[:n])
		if err == nil && len(m.Response) > len(full) {
			t.Fatalf("cut at %d: %+v", n, m)
		}
	}
	for _, bad := range [][]byte{
		{0x72, 0xff, 0xff, 0xff, 0xff, 0x0f}, // a length far past the end
		{0x73},                               // a field kind that does not exist (3)
		{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, // a number that never ends
	} {
		if _, err := Decode(bad); err == nil {
			t.Errorf("% x: no error", bad)
		}
	}
}
