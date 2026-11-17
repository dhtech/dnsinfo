#!/bin/sh
# Builds what a release carries, into dist/:
#
#   dnsinfo_<version>_linux_<arch>.tar.gz   the binary, the unit, README
#   dnsinfo_<version>_<debarch>.deb         the same as a Debian package
#   SHA256SUMS
#
# Both carry THIRD-PARTY-LICENSES: the licences of the libraries that
# are compiled into the binary, as their terms ask.
#
#   sh .github/build.sh 1.2.3
#
# Needs Go, tar and dpkg-deb. The binary is static (no C library), so
# one build serves every Debian release.
set -eu

version=${1:?the version, like 1.2.3}
# three numbers and nothing else: what a tag vX.Y.Z gives. (Anything
# after them - "-rc1" - would sort as NEWER than the release itself in
# dpkg's eyes.)
if ! printf '%s\n' "$version" | grep -Eqx '[0-9]+\.[0-9]+\.[0-9]+'; then
    echo "the version is three numbers, like 1.2.3 - not: $version"
    exit 1
fi

# in the repository, wherever it was called from: build/ and dist/
# below are removed first
cd "$(dirname "$0")/.."
test -f go.mod
rm -rf build dist
mkdir -p build dist

# the licences of every module that is compiled into the binary
third=build/THIRD-PARTY-LICENSES
echo "dnsinfo is built with the libraries below. These are their licences." > "$third"
GOOS=linux GOARCH=amd64 go list -deps \
    -f '{{with .Module}}{{if not .Main}}{{.Path}} {{.Version}} {{.Dir}}{{end}}{{end}}' \
    ./cmd/dnsinfo | sort -u > build/modules
test -s build/modules
while read -r path ver dir; do
    found=0
    for f in "$dir"/LICENSE* "$dir"/COPYING* "$dir"/COPYRIGHT* \
        "$dir"/NOTICE* "$dir"/PATENTS*; do
        [ -f "$f" ] || continue
        found=1
        printf '\n\n======== %s %s: %s\n\n' "$path" "$ver" "$(basename "$f")" >> "$third"
        cat "$f" >> "$third"
    done
    if [ "$found" = 0 ]; then
        echo "no licence file found in $path $ver ($dir)"
        exit 1
    fi
done < build/modules
echo "third-party licences: $(wc -l < build/modules) modules"
for arch in amd64 arm64; do
    name="dnsinfo_${version}_linux_${arch}"
    mkdir -p "build/${name}"
    CGO_ENABLED=0 GOOS=linux GOARCH="${arch}" go build -trimpath \
        -ldflags "-s -w -X main.version=${version}" \
        -o "build/${name}/dnsinfo" ./cmd/dnsinfo
    cp LICENSE README.md packaging/dnsinfo.service "$third" "build/${name}/"
    tar -C build --owner=0 --group=0 --numeric-owner \
        -czf "dist/${name}.tar.gz" "${name}"

    # the package's tree (Go's and Debian's names for the two
    # architectures are the same)
    root="build/deb-${arch}"
    install -D -m 755 "build/${name}/dnsinfo" "$root/usr/sbin/dnsinfo"
    install -D -m 644 packaging/dnsinfo.service \
        "$root/usr/lib/systemd/system/dnsinfo.service"
    install -D -m 644 LICENSE "$root/usr/share/doc/dnsinfo/copyright"
    install -D -m 644 README.md "$root/usr/share/doc/dnsinfo/README.md"
    install -D -m 644 "$third" \
        "$root/usr/share/doc/dnsinfo/THIRD-PARTY-LICENSES"
    size=$(du -sk "$root" | cut -f1)
    mkdir "$root/DEBIAN"
    sed -e "s/@VERSION@/$version/" -e "s/@ARCH@/$arch/" \
        -e "s/@SIZE@/$size/" packaging/control.in > "$root/DEBIAN/control"
    if grep -q '@[A-Z_]*@' "$root/DEBIAN/control"; then
        echo "packaging/control.in: something was not filled in"
        exit 1
    fi
    install -m 755 packaging/postinst packaging/prerm packaging/postrm \
        "$root/DEBIAN/"
    # xz: read by every dpkg (a newer dpkg-deb would choose zstd)
    dpkg-deb -Zxz --root-owner-group --build "$root" \
        "dist/dnsinfo_${version}_${arch}.deb"
done
(cd dist && sha256sum -- * > SHA256SUMS)
dpkg-deb --info "dist/dnsinfo_${version}_amd64.deb"
dpkg-deb --contents "dist/dnsinfo_${version}_amd64.deb"
