#!/usr/bin/env sh
set -eu

PKG="${PKG:-./cmd/chargeghost}"
LDFLAGS="${LDFLAGS:--s -w}"
BASE_NAME="${BASE_NAME:-chargeghost-core}"

GOOS="${GOOS:-$(go env GOOS)}"
GOARCH="${GOARCH:-$(go env GOARCH)}"

target_triple() {
	if [ -n "${TARGET_TRIPLE:-}" ]; then
		printf '%s' "$TARGET_TRIPLE"
		return
	fi

	case "$GOOS/$GOARCH" in
	linux/amd64) printf '%s' 'x86_64-unknown-linux-gnu' ;;
	linux/arm64) printf '%s' 'aarch64-unknown-linux-gnu' ;;
	linux/arm) printf '%s' 'armv7-unknown-linux-gnueabihf' ;;
	darwin/amd64) printf '%s' 'x86_64-apple-darwin' ;;
	darwin/arm64) printf '%s' 'aarch64-apple-darwin' ;;
	windows/amd64) printf '%s' 'x86_64-pc-windows-msvc' ;;
	windows/arm64) printf '%s' 'aarch64-pc-windows-msvc' ;;
	*)
		if command -v rustc >/dev/null 2>&1 \
			&& [ "$GOOS" = "$(go env GOOS)" ] \
			&& [ "$GOARCH" = "$(go env GOARCH)" ]; then
			rustc --print host-tuple
		else
			echo "unsupported GOOS/GOARCH: $GOOS/$GOARCH (set TARGET_TRIPLE)" >&2
			exit 1
		fi
		;;
	esac
}

TRIPLE=$(target_triple)
EXT=""
[ "$GOOS" = "windows" ] && EXT=".exe"

if [ -n "${OUT:-}" ]; then
	OUTPUT="$OUT"
else
	OUTPUT="${BASE_NAME}-${TRIPLE}${EXT}"
fi

printf 'Building %s from %s (GOOS=%s GOARCH=%s)\n' "$OUTPUT" "$PKG" "$GOOS" "$GOARCH"
CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build -ldflags="$LDFLAGS" -o "$OUTPUT" "$PKG"
printf 'Build complete: %s\n' "$OUTPUT"
