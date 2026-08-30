#!/bin/sh

set -eu

destination=${1:-}
if [ -z "$destination" ]; then
	printf '%s\n' 'usage: package-licenses.sh DESTINATION' >&2
	exit 2
fi

mkdir -p "$destination"
module_list=$(mktemp)
trap 'rm -f "$module_list"' EXIT HUP INT TERM

go list -deps -f '{{if .Module}}{{if not .Module.Main}}{{.Module.Path}}|{{.Module.Dir}}{{end}}{{end}}' ./cmd/elgatolight \
	| LC_ALL=C sort -u >"$module_list"

manifest="$destination/README.md"
printf '%s\n\n%s\n\n' '# Third-party licenses' 'Elgato Key Light Neo USB controller binary distributions include the following Go modules. Their license and notice files are copied verbatim beside this manifest.' >"$manifest"

while IFS='|' read -r module_path module_directory; do
	[ -n "$module_path" ] || continue
	module_name=$(printf '%s' "$module_path" | sed 's#[/:]#__#g')
	module_destination="$destination/$module_name"
	mkdir -p "$module_destination"
	found=false
	for candidate in LICENSE LICENSE.txt LICENSE.md COPYING COPYING.txt NOTICE NOTICE.txt; do
		if [ -f "$module_directory/$candidate" ]; then
			cp "$module_directory/$candidate" "$module_destination/$candidate"
			found=true
		fi
	done
	if [ "$found" != true ]; then
		printf 'error: no license file found for %s in %s\n' "$module_path" "$module_directory" >&2
		exit 1
	fi
	printf -- '- `%s` — `%s/`\n' "$module_path" "$module_name" >>"$manifest"
done <"$module_list"
