#!/bin/sh

set -eu

action=${1:-}
if [ -z "$action" ]; then
	echo "usage: $0 tidy|verify|vet|test [go test arguments...]" >&2
	exit 2
fi
shift

root_dir=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)

tidy_module() {
	module_dir=$1
	if [ "$module_dir" = "." ]; then
		(cd "$module_dir" && go mod tidy)
		return
	fi
	(
		cd "$module_dir"
		go mod edit -replace=github.com/levskiy0/webpprof@v0.2.0="$root_dir"
		trap 'go mod edit -dropreplace=github.com/levskiy0/webpprof@v0.2.0' EXIT
		go mod tidy
	)
}

find . -name go.mod -not -path './.git/*' -print | sort | while IFS= read -r module_file; do
	module_dir=$(dirname "$module_file")
	echo "==> $action $module_dir"
	case "$action" in
		tidy)
			tidy_module "$module_dir"
			;;
		verify)
			(cd "$module_dir" && go mod verify)
			;;
		vet)
			(cd "$module_dir" && go vet ./...)
			;;
		test)
			(cd "$module_dir" && go test "$@" ./...)
			;;
		*)
			echo "unknown action: $action" >&2
			exit 2
			;;
	esac
done
