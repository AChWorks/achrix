#!/usr/bin/env bash
# SPDX-License-Identifier: MPL-2.0
# Install exact native validation tools in an explicitly supplied empty/user-owned
# directory. No system service, host database or production deployment is changed.
set -euo pipefail
if [[ $# != 1 || -z $1 || -e $1 ]]; then
  echo 'Usage: setup-validation-postgres.sh /absolute/new/owned/directory' >&2; exit 1
fi
case "$1" in /*) ;; *) echo 'An absolute directory is required' >&2; exit 1;; esac
for tool in curl sha256sum tar bzip2 gcc make bison flex m4; do command -v "$tool" >/dev/null; done
mkdir -p "$1"
destination=$(cd "$1" && pwd)
cd "$destination"
curl --fail --silent --show-error --location --output postgresql-18.6.tar.bz2 \
  https://ftp.postgresql.org/pub/source/v18.6/postgresql-18.6.tar.bz2
echo '555610c24d53e4316da5b7d3fc25c279d96856d5e0e23ee308c328c5fa881d9f  postgresql-18.6.tar.bz2' | sha256sum -c -
tar -xjf postgresql-18.6.tar.bz2
cd postgresql-18.6
./configure --prefix="$destination/install" --without-icu --without-readline --without-zlib > "$destination/configure.log"
make -j2 > "$destination/build.log"
make install > "$destination/install.log"
"$destination/install/bin/postgres" --version
echo "Validation binaries: $destination/install/bin"
