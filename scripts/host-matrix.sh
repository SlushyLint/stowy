#!/bin/sh
set -eu

if [ "$#" -ne 2 ]; then
	printf 'usage: %s <debian|fedora|arch> <image>\n' "$0" >&2
	exit 2
fi

distro=$1
image=$2
repo=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)

docker run --rm \
	-e STOWY_MATRIX_DISTRO="$distro" \
	-v "$repo:/workspace:ro" \
	-w /workspace \
	"$image" sh -euc '
	case "$STOWY_MATRIX_DISTRO" in
		debian)
			apt-get update
			apt-get install -y ca-certificates golang-go
			;;
		fedora)
			dnf install -y ca-certificates golang
			;;
		arch)
			pacman -Sy --noconfirm ca-certificates go
			;;
		*)
			echo "unsupported matrix distro: $STOWY_MATRIX_DISTRO" >&2
			exit 2
			;;
	esac

	mkdir -p "$HOME/.local/bin"
	go build -o "$HOME/.local/bin/stowy" .
	"$HOME/.local/bin/stowy" i ripgrep
	PATH="$HOME/.local/bin:$PATH" "$HOME/.local/bin/stowy" run rg --version
'
