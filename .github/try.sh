#!/bin/sh
# Tries the package build.sh left in dist/, as a server would have it.
# Runs INSIDE a NEW debian:13 container that has nothing of the build:
#
#   docker run --rm -v "$PWD:/src:ro" -w /src debian:13 \
#       sh .github/try.sh 1.2.3
#
# The package installs without fetching anything (it depends on
# nothing); the program says the version; the unit is there and is
# neither enabled nor started; the program, started the way the unit
# starts it but for systemd's own part, makes its socket for the
# group; and the package can be removed again.
set -eu

version=${1:?the version, like 1.2.3}
deb=$(ls dist/dnsinfo_*_"$(dpkg --print-architecture)".deb)
dpkg -i "$deb"
dpkg -s dnsinfo | grep -E '^(Status|Version):'
test "$(dpkg-query -W -f='${Version}' dnsinfo)" = "$version"
said=$(dnsinfo -version)
echo "$said"
case "$said" in
    "dnsinfo $version "*) ;;
    *) echo "the program does not say version $version"; exit 1 ;;
esac

unit=/usr/lib/systemd/system/dnsinfo.service
test -f "$unit"
grep -qxF 'ExecStart=/usr/sbin/dnsinfo -socket /run/dnsinfo/dnstap.sock -socket-group bind $DNSINFO_OPTS' "$unit"
# nothing was enabled
test ! -e /etc/systemd/system/multi-user.target.wants/dnsinfo.service

# the unit's way, by hand: a directory under /run, the group, no
# database to be had (it keeps trying; that is not this test)
groupadd --system bind
mkdir -p /run/dnsinfo
dnsinfo -socket /run/dnsinfo/dnstap.sock -socket-group bind \
    -dsn 'host=127.0.0.1 port=1 dbname=x user=x connect_timeout=1' \
    -metrics /tmp/dnsinfo.prom > /tmp/dnsinfo.log 2>&1 &
pid=$!
sleep 2
test "$(stat -c '%G %a' /run/dnsinfo/dnstap.sock)" = "bind 660"
grep -q '^dnsinfo_connected 0$' /tmp/dnsinfo.prom
kill "$pid"
wait "$pid" || true
test ! -e /run/dnsinfo/dnstap.sock
echo "socket made for the group, metrics written, socket gone at stop"

dpkg -r dnsinfo
test ! -e /usr/sbin/dnsinfo
echo "package tried: ok"
