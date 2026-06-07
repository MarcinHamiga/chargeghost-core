#!/usr/bin/env sh
set -eu

OUT="${OUT:-chargeghost}"
PKG="${PKG:-./cmd/chargeghost}"
LDFLAGS="${LDFLAGS:--s -w}"

printf 'Building %s from %s\n' "$OUT" "$PKG"
CGO_ENABLED=0 go build -ldflags="$LDFLAGS" -o "$OUT" "$PKG"
printf 'Build complete: %s\n' "$OUT"
