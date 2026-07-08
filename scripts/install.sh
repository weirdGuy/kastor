#!/bin/sh
# Install the latest kastor release from GitHub.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/weirdGuy/kastor/main/scripts/install.sh | sh
#
# Environment:
#   KASTOR_INSTALL_DIR  Override the install directory. Defaults to
#                       /usr/local/bin when writable, otherwise ~/.local/bin.
#                       Never invokes sudo.
set -eu

REPO="weirdGuy/kastor"

err() {
    echo "install.sh: $1" >&2
    exit 1
}

command -v curl >/dev/null 2>&1 || err "curl is required"
command -v tar >/dev/null 2>&1 || err "tar is required"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
    linux | darwin) ;;
    *) err "unsupported OS: $os (on Windows, download the zip from https://github.com/${REPO}/releases)" ;;
esac

arch=$(uname -m)
case "$arch" in
    x86_64 | amd64) arch=amd64 ;;
    aarch64 | arm64) arch=arm64 ;;
    *) err "unsupported architecture: $arch" ;;
esac

tag=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" |
    grep -o '"tag_name": *"[^"]*"' | head -n 1 | cut -d '"' -f 4)
[ -n "$tag" ] || err "could not determine the latest release tag"
version=${tag#v}

archive="kastor_${version}_${os}_${arch}.tar.gz"
base_url="https://github.com/${REPO}/releases/download/${tag}"

tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT INT TERM

echo "Downloading kastor ${tag} (${os}/${arch})..."
curl -fsSL -o "${tmpdir}/${archive}" "${base_url}/${archive}"
curl -fsSL -o "${tmpdir}/checksums.txt" "${base_url}/checksums.txt"

checksum_line=$(grep "[[:space:]]${archive}\$" "${tmpdir}/checksums.txt") ||
    err "no entry for ${archive} in checksums.txt"
if command -v sha256sum >/dev/null 2>&1; then
    (cd "$tmpdir" && echo "$checksum_line" | sha256sum -c - >/dev/null) ||
        err "checksum verification failed for ${archive}"
elif command -v shasum >/dev/null 2>&1; then
    (cd "$tmpdir" && echo "$checksum_line" | shasum -a 256 -c - >/dev/null) ||
        err "checksum verification failed for ${archive}"
else
    err "sha256sum or shasum is required to verify the download"
fi

tar -xzf "${tmpdir}/${archive}" -C "$tmpdir" kastor

if [ -n "${KASTOR_INSTALL_DIR:-}" ]; then
    install_dir=$KASTOR_INSTALL_DIR
    mkdir -p "$install_dir"
elif [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
    install_dir=/usr/local/bin
else
    install_dir="${HOME}/.local/bin"
    mkdir -p "$install_dir"
fi

cp "${tmpdir}/kastor" "${install_dir}/kastor"
chmod 0755 "${install_dir}/kastor"
echo "Installed kastor ${tag} to ${install_dir}/kastor"

case ":${PATH}:" in
    *":${install_dir}:"*) ;;
    *) echo "Note: ${install_dir} is not on your PATH" >&2 ;;
esac
