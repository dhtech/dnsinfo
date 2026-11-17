// SPDX-License-Identifier: GPL-3.0-or-later

// Package domain cuts a name down to the domain it belongs to:
// www.cdn.example.org -> example.org.
package domain

import (
	"strings"

	"golang.org/x/net/publicsuffix"
)

// Of returns the domain of name (small letters, no dot at the end, as
// dnsmsg gives it).
//
// "The domain" is one label more than the public suffix - the part
// somebody registered: example.org, example.co.uk. The list of public
// suffixes is built into the program; nothing is fetched.
//
//   - a name that IS a public suffix (com, co.uk), or has one label
//     (localhost), stays as it is;
//   - a name under a top level domain nobody knows (ours: host.site.local)
//     gets the usual two labels: site.local;
//   - reverse lookups count as two domains, in-addr.arpa and ip6.arpa,
//     not as one per address block;
//   - what is no name ("(invalid)", ".") stays as it is.
func Of(name string) string {
	switch {
	case name == "" || name == "." || strings.HasPrefix(name, "("):
		return name
	case name == "in-addr.arpa" || strings.HasSuffix(name, ".in-addr.arpa"):
		return "in-addr.arpa"
	case name == "ip6.arpa" || strings.HasSuffix(name, ".ip6.arpa"):
		return "ip6.arpa"
	}
	d, err := publicsuffix.EffectiveTLDPlusOne(name)
	if err != nil {
		return name
	}
	return d
}
