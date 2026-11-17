#!/bin/sh
# Builds what a release carries, into dist/:
#
#   dnsinfo_<version>_linux_<arch>.tar.gz   the binary, the unit, README
#   dnsinfo_<version>_<debarch>.deb         the same as a Debian package
#   SHA256SUMS
#
#   sh .github/build.sh 1.2.3
#
# Needs Go, tar and dpkg-deb. The binary is static (no C library), so
# one build serves every Debian release; the package has no
# dependencies.
set -eu

version=${1:?the version, like 1.2.3}
case "$version" in
    [0-9]*) ;;
    *) echo "a Debian version starts with a digit: $version"; exit 1 ;;
esac

rm -rf build dist
mkdir -p dist
for arch in amd64 arm64; do
    name="dnsinfo_${version}_linux_${arch}"
    mkdir -p "build/${name}"
    CGO_ENABLED=0 GOOS=linux GOARCH="${arch}" go build -trimpath \
        -ldflags "-s -w -X main.version=${version}" \
        -o "build/${name}/dnsinfo" ./cmd/dnsinfo
    cp LICENSE README.md packaging/dnsinfo.service "build/${name}/"
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
