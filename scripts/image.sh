#!/bin/sh
# Prints the pinned reference of one of the build's tool images, by its name
# in tools/images/Dockerfile (pandoc, redocly, syft), where Dependabot keeps
# them current.
#
#   sh scripts/image.sh <name>
set -eu

name=${1:?usage: image.sh <name>}
ref=$(awk -v name="$name" 'toupper($1) == "FROM" && toupper($3) == "AS" && $4 == name { print $2 }' \
	"$(dirname "$0")/../tools/images/Dockerfile")
if [ -z "$ref" ]; then
	echo "image.sh: no image named $name in tools/images/Dockerfile" >&2
	exit 1
fi
echo "$ref"
