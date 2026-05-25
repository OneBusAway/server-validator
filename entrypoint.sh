#!/bin/sh
# Render runs a one-off job's startCommand by splitting it on whitespace and
# passing the result as argv — there is no shell, so quotes are not stripped and
# a JSON config cannot be passed inline (it has spaces and special characters).
# Callers therefore base64-encode the compact config JSON into a single token:
#
#   startCommand: /app/entrypoint.sh <base64-config-json> [validator-flags...]
#
# This script decodes that token and hands the raw JSON to the validator as its
# sole positional argument. A raw JSON argument (one starting with "{") is passed
# through unchanged, which keeps local `docker run` ergonomic; base64 strings
# never start with "{", so the two cases are unambiguous. Any extra arguments are
# forwarded to the validator as flags (which must precede the config).
set -eu

if [ "$#" -eq 0 ]; then
	echo "usage: entrypoint.sh <base64-config-json | raw-json> [flags...]" >&2
	exit 2
fi

arg="$1"
shift
case "$arg" in
	'{'*) config="$arg" ;;
	*)    config="$(printf '%s' "$arg" | base64 -d)" ;;
esac

exec /app/oba-validator "$@" "$config"
