// SPDX-License-Identifier: GPL-3.0-or-later

package domain

import "testing"

func TestOf(t *testing.T) {
	for name, want := range map[string]string{
		"www.example.org":                  "example.org",
		"example.org":                      "example.org",
		"a.b.c.d.example.com":              "example.com",
		"www.example.co.uk":                "example.co.uk",
		"co.uk":                            "co.uk",
		"org":                              "org",
		"localhost":                        "localhost",
		"server.site.example.local":        "example.local",
		"printer.lan":                      "printer.lan",
		"7.2.0.192.in-addr.arpa":           "in-addr.arpa",
		"in-addr.arpa":                     "in-addr.arpa",
		"1.0.0.0.8.b.d.0.1.0.0.2.ip6.arpa": "ip6.arpa",
		"_sip._tcp.example.org":            "example.org",
		".":                                ".",
		"":                                 "",
		"(invalid)":                        "(invalid)",
	} {
		if got := Of(name); got != want {
			t.Errorf("Of(%q) = %q, want %q", name, got, want)
		}
	}
}
