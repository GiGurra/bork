#!/bin/sh
# Build the shared grammar for a local tree-sitter consumer.
set -eu
if [ "$#" -ne 1 ]; then echo 'usage: editors/install-parser.sh /absolute/output/parser.so' >&2; exit 2; fi
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
source_dir=${BORK_GRAMMAR_DIR:-"$root/editors/tree-sitter-bork"}
mkdir -p -- "$(dirname -- "$1")"
case $(uname -s) in
  Darwin) set -- "$1" -dynamiclib ;;
  *) set -- "$1" -shared ;;
esac
output=$1
shift
"${CC:-cc}" "$@" -fPIC -O2 -I "$source_dir/src" "$source_dir/src/parser.c" "$source_dir/src/scanner.c" -o "$output"
