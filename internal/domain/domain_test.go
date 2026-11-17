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
		// the list's private part is not used: a company's customers
		// count as the company
		"d111111abcdef8.cloudfront.net":                     "cloudfront.net",
		"cloudfront.net":                                    "cloudfront.net",
		"my-bucket.s3.eu-north-1.amazonaws.com":             "amazonaws.com",
		"s3.eu-north-1.amazonaws.com":                       "amazonaws.com",
		"ec2-198-51-100-7.eu-north-1.compute.amazonaws.com": "amazonaws.com",
		"alice.github.io":                                   "github.io",
		"someblog.blogspot.com":                             "blogspot.com",
		"shop.example.blogspot.co.uk":                       "blogspot.co.uk",
		"e1234.a.akamaiedge.net":                            "akamaiedge.net",
		".":                                                 ".",
		"":                                                  "",
		"(invalid)":                                         "(invalid)",
	} {
		if got := Of(name); got != want {
			t.Errorf("Of(%q) = %q, want %q", name, got, want)
		}
	}
}
