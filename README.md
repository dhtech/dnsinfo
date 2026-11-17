# dnsinfo

Counts which domains a name server is asked for.

The name server (BIND 9 with dnstap) sends every answer it gives a
client to a unix socket. `dnsinfo` listens there, reads the name that
was asked for out of each answer, cuts it down to its domain -
`example.org` for `www.cdn.example.org` - and adds one to that domain's
counter for the hour, in a PostgreSQL database.

**Nothing else is kept: not who asked, not the full name, not the
answer.** The client's address is in every dnstap message; `dnsinfo`
never reads it out.

## What is in the database

```
 domain        | hour                   | result   | answers
---------------+------------------------+----------+---------
 example.org   | 2026-01-02 10:00:00+00 | answered |    1842
 example.org   | 2026-01-02 10:00:00+00 | noname   |      17
 example.co.uk | 2026-01-02 10:00:00+00 | answered |     311
```

- `dnsinfo_domain`: one row per domain, hour and result. `result` is
  `answered`, `noname` (no such name) or `failed` (everything else).
- `dnsinfo_total`: a view with every domain's count over all hours.

```sql
-- the top 20 of the whole time
SELECT domain, answers FROM dnsinfo_total ORDER BY answers DESC LIMIT 20;

-- the top 20 right now
SELECT domain, sum(answers) AS answers FROM dnsinfo_domain
WHERE hour = date_trunc('hour', now()) GROUP BY domain ORDER BY answers DESC LIMIT 20;

-- what fails most
SELECT domain, failed FROM dnsinfo_total WHERE failed > 0 ORDER BY failed DESC LIMIT 20;
```

The program makes its tables itself the first time it starts. The
database only has to provide a role that may create tables in it.

Several name servers can write to the same database: their counts add
up.

## How a name becomes a domain

One label more than the public suffix - the part somebody registered:
`www.example.org` -> `example.org`, `www.example.co.uk` ->
`example.co.uk`. The list of public suffixes is built into the program;
nothing is fetched when it runs.

Only the official part of that list is used - the registries' own
suffixes. Its private part, where companies have entered the names they
hand out to customers, is not: `d111111abcdef8.cloudfront.net` counts as
`cloudfront.net`, `alice.github.io` as `github.io`, a bucket under
`s3.eu-north-1.amazonaws.com` as `amazonaws.com`. A top list then shows
the company, not thousands of its customers one by one.

- reverse lookups count as `in-addr.arpa` and `ip6.arpa`;
- a name under a top level domain nobody knows (`host.site.internal`)
  keeps its last two labels;
- a name that is no host name (other bytes than letters, digits, `-`
  and `_`) counts as `(invalid)`;
- when an hour has more different domains than `-max-domains`, the rest
  of that hour counts as `(other)` - a million made-up domains fill one
  row, not a million.

## Running it

```
dnsinfo -socket /run/dnsinfo/dnstap.sock -socket-group bind
```

with the database in the environment:

```
DNSINFO_DSN=host=db.example.net dbname=dnsinfo user=dnsinfo sslmode=verify-full sslrootcert=... sslcert=... sslkey=...
```

and in `named.conf`:

```
options {
    dnstap { client response; };
    dnstap-output unix "/run/dnsinfo/dnstap.sock";
};
```

The name server must be able to write to the socket (`-socket-group`),
and where it is confined by AppArmor or SELinux it must be allowed that
path.

### The Debian package

A release carries `dnsinfo_<version>_<arch>.deb`: the binary as
`/usr/sbin/dnsinfo` and the unit `dnsinfo.service`. It makes a user
`dnsinfo` for the program and depends on `bind9`, for the group `bind`.
Installing it starts nothing; give it its database and start it:

```
echo 'DNSINFO_DSN=host=db.example.net dbname=dnsinfo user=dnsinfo sslmode=verify-full' > /etc/default/dnsinfo
chmod 600 /etc/default/dnsinfo
systemctl enable --now dnsinfo
```

The unit listens on `/run/dnsinfo/dnstap.sock` and gives the socket to
the group `bind`. It runs as the user `dnsinfo`, with no privileges;
the one place it can write to is `/run/dnsinfo`. If nothing was given
to start it with it ends once, with status 78, and is not started
again. More options go in `DNSINFO_OPTS`, in
the same file or in a drop-in - for the metrics, for example,
`DNSINFO_OPTS=-metrics /run/dnsinfo/dnsinfo.prom`, with a link to that
file where the node exporter reads its text files.

The same binary without a package is `dnsinfo_<version>_linux_<arch>.tar.gz`.

| Option | Default | |
|---|---|---|
| `-socket` | `/run/dnsinfo/dnstap.sock` | where the name server sends its stream |
| `-socket-group` | | give the socket to this group |
| `-dsn` | `$DNSINFO_DSN` | the PostgreSQL connection string or URL |
| `-flush` | `5s` | how often the counters are added to the database |
| `-max-domains` | `100000` | different domains per hour, before `(other)` |
| `-max-held` | `500000` | counters held in memory while the database is away |
| `-metrics` | | write Prometheus metrics to this file (node exporter textfile collector) |

## When things are away

- **The database:** the program keeps counting in memory and adds
  everything when the database is back. If that takes so long that
  `-max-held` counters wait, further answers are not counted, and
  `dnsinfo_dropped_total` says how many.
- **dnsinfo itself:** the name server does not wait for it. It drops
  what it would have sent, goes on answering, and connects again when
  the socket is back.
- A stopped `dnsinfo` loses at most the counts of the last `-flush`.

## Metrics

`dnsinfo_connected`, `dnsinfo_streams`,
`dnsinfo_last_success_timestamp_seconds`, `dnsinfo_messages_total`,
`dnsinfo_answers_total`, `dnsinfo_other_total`,
`dnsinfo_unreadable_total`, `dnsinfo_dropped_total`,
`dnsinfo_written_total`, `dnsinfo_errors_total`, `dnsinfo_held`.

## Building

```
go test ./...
go build ./cmd/dnsinfo
```

The tests of the database part need a PostgreSQL they may fill and
empty: `DNSINFO_TEST_DSN=postgres://...`. Without it they are skipped.

`sh .github/build.sh 1.2.3` builds what a release carries (it needs
`dpkg-deb`); a tag `vX.Y.Z` does that on GitHub, for linux amd64 and
arm64.

## What it reads, and how

The name server's stream is read with the protocols' own libraries;
nothing of it is parsed by code of ours:

- Frame Streams, the transport: `github.com/farsightsec/golang-framestream`;
- dnstap's protocol buffer: `github.com/dnstap/golang-dnstap`;
- the DNS message inside: `golang.org/x/net/dns/dnsmessage`.

What is ours is what happens to a name once it is read: `internal/dnsmsg`
(which answers count, and as what), `internal/domain` (the name cut to
its domain), `internal/count` (the counters) and `internal/store` (the
database).

## License

GPL-3.0-or-later.
