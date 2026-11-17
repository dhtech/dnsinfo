#!/bin/sh
# Tries the package build.sh left in dist/, as a server would have it.
# Runs INSIDE a NEW debian:13 container that has nothing of the build:
#
#   docker run --rm -v "$PWD:/src:ro" -w /src debian:13 \
#       sh .github/try.sh 1.2.3
#
# The package installs with apt, which fetches what it depends on
# (bind9, for the group "bind"); the program says the version; the unit
# is there, systemd finds no fault in it, and it is neither enabled nor
# started; the package's user "dnsinfo" is there; the program, started
# as that user in the group "bind" - what the unit makes of it - makes
# its socket for the group and writes its numbers; and the package can
# be removed again, its user with it at a purge.
#
# NOT tried here: the unit started BY systemd (its sandbox, the
# credentials of a drop-in), and the package's restart of a running
# dnsinfo at an upgrade - a container like this one has no systemd
# running in it.
set -eu

version=${1:?the version, like 1.2.3}
export DEBIAN_FRONTEND=noninteractive
apt-get update -q
apt-get install -y -q --no-install-recommends systemd \
    ./dist/dnsinfo_*_"$(dpkg --print-architecture)".deb
dpkg -s dnsinfo | grep -E '^(Status|Version):'
# from here on every step is shown: one that fails is the last one seen
set -x
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
# systemd reads the unit and finds no fault in it
systemd-analyze verify "$unit"
# (a "slim" image leaves /usr/share/doc out of every package: not there)
test -s /usr/share/doc/dnsinfo/THIRD-PARTY-LICENSES

# started without a database it says so and ends with 78 - which the
# unit does not start again
rc=0
env -u DNSINFO_DSN dnsinfo -socket /tmp/none.sock 2> /tmp/nodsn.log || rc=$?
cat /tmp/nodsn.log
test "$rc" = 78
grep -qxF 'RestartPreventExitStatus=78' "$unit"
# nothing was enabled
test ! -e /etc/systemd/system/multi-user.target.wants/dnsinfo.service

# the unit's way, by hand: the package's user, in the group bind (which
# came with bind9), a directory of its own under /run, no database to
# be had (it keeps trying; that is not this test)
getent passwd dnsinfo
getent group bind >/dev/null
grep -qxF 'User=dnsinfo' "$unit"
mkdir -p /run/dnsinfo
chown dnsinfo /run/dnsinfo
setpriv --reuid dnsinfo --regid dnsinfo --groups bind \
    --bounding-set=-all --inh-caps=-all --no-new-privs \
    dnsinfo -socket /run/dnsinfo/dnstap.sock -socket-group bind \
    -dsn 'host=127.0.0.1 port=1 dbname=x user=x connect_timeout=1' \
    -metrics /run/dnsinfo/dnsinfo.prom > /tmp/dnsinfo.log 2>&1 &
pid=$!
# when it fails from here on, show what the program said
trap 'rc=$?; [ "$rc" = 0 ] || { echo "--- the program said:"; cat /tmp/dnsinfo.log; }' EXIT
# the socket is there at once; the numbers after the first try at the
# database. Waited for, not guessed at: at most 30 seconds.
n=0
until [ -S /run/dnsinfo/dnstap.sock ] && [ -s /run/dnsinfo/dnsinfo.prom ]; do
    n=$((n + 1))
    [ "$n" -le 150 ] || { echo "no socket or no numbers after 30 s"; exit 1; }
    sleep 0.2
done
test "$(stat -c '%U %G %a' /run/dnsinfo/dnstap.sock)" = "dnsinfo bind 660"
grep -E '^Cap(Prm|Eff|Bnd):[[:space:]]+0+$' "/proc/$pid/status" | wc -l | grep -qx 3
grep -q '^dnsinfo_connected 0$' /run/dnsinfo/dnsinfo.prom
kill "$pid"
wait "$pid" || true
test ! -e /run/dnsinfo/dnstap.sock
echo "socket made for the group, metrics written, socket gone at stop"

# removed: the program goes, somebody's settings stay; purged: they go too
echo 'DNSINFO_DSN=host=db.example.net' > /etc/default/dnsinfo
dpkg -r dnsinfo
test ! -e /usr/sbin/dnsinfo
test -e /etc/default/dnsinfo
dpkg -P dnsinfo
test ! -e /etc/default/dnsinfo
if getent passwd dnsinfo; then
    echo "the user dnsinfo is still there after the purge"
    exit 1
fi
echo "package tried: ok"
