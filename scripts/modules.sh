#!/bin/sh

set -eu

action=${1:-}
if [ -z "$action" ]; then
	echo "usage: $0 tidy|verify|vet|test|vuln [arguments...]" >&2
	exit 2
fi
shift

root_dir=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)

run_with_local_core() {
	module_dir=$1
	shift
	if [ "$module_dir" = "." ]; then
		(cd "$module_dir" && GOWORK=off "$@")
		return
	fi
	(
		cd "$module_dir"
		go mod edit -replace=github.com/levskiy0/webpprof="$root_dir"
		if grep -F 'github.com/levskiy0/webpprof/storage/sqlite ' go.mod >/dev/null; then
			go mod edit -replace=github.com/levskiy0/webpprof/storage/sqlite="$root_dir/storage/sqlite"
			trap 'go mod edit -dropreplace=github.com/levskiy0/webpprof/storage/sqlite; go mod edit -dropreplace=github.com/levskiy0/webpprof' EXIT
		else
			trap 'go mod edit -dropreplace=github.com/levskiy0/webpprof' EXIT
		fi
		GOWORK=off "$@"
	)
}

find . -name go.mod -not -path './.git/*' -print | sort | while IFS= read -r module_file; do
	module_dir=$(dirname "$module_file")
	echo "==> $action $module_dir"
	case "$action" in
		tidy)
			run_with_local_core "$module_dir" go mod tidy
			;;
		verify)
			run_with_local_core "$module_dir" go mod verify
			;;
		vet)
			run_with_local_core "$module_dir" go vet ./...
			;;
		test)
			run_with_local_core "$module_dir" go test "$@" ./...
			;;
		vuln)
			run_with_local_core "$module_dir" "${GOVULNCHECK:-govulncheck}" "$@" ./...
			;;
		*)
			echo "unknown action: $action" >&2
			exit 2
			;;
	esac
done
