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
// Only the OFFICIAL part of that list counts (the registries' own
// suffixes: org, co.uk). Its private part - names a company handed in
// because it gives its customers names below them: cloudfront.net,
// github.io, s3.eu-north-1.amazonaws.com - is not used, so that every
// customer of such a company is counted as that company's domain
// (cloudfront.net, github.io, amazonaws.com) and not as one of its own.
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
	// the suffix and one label more - as many labels of the name as
	// that, counted from its end
	labels := strings.Count(officialSuffix(name), ".") + 2
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '.' {
			if labels--; labels == 0 {
				return name[i+1:]
			}
		}
	}
	return name
}

// officialSuffix is the public suffix of name by the official part of
// the list. The library answers with the longest suffix of either part
// and says which part it is from; under a private one the official one
// is found by asking again, one label further in each time. A top
// level domain the list does not know at all is its own suffix.
func officialSuffix(name string) string {
	for {
		suffix, official := publicsuffix.PublicSuffix(name)
		dot := strings.IndexByte(suffix, '.')
		if official || dot < 0 {
			return suffix
		}
		name = suffix[dot+1:]
	}
}
