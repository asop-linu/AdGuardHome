#!/bin/sh

# This comment is used to simplify checking local copies of the script.  Bump
# this number every time a significant change is made to this script.
#
# AdGuard-Project-Version: 6

# Don't use -f, because we use globs in this script.
set -e -u

# pipefail is not supported by strict POSIX sh (e.g. dash), so enable it only
# when the current shell understands it.  The Makefile invokes this script
# with "$(SHELL)", which may be dash.  The option is probed in a subshell,
# because a failing set is fatal in dash even with || true.
if (set -o pipefail 2>/dev/null); then
	set -o pipefail
fi

verbose="${VERBOSE:-0}"
readonly verbose

if [ "$verbose" -gt '0' ]; then
	set -x
fi

# Source the common helpers, including not_found and run_linter.
. ./scripts/make/helper.sh

go="${GO:-go}"
readonly go

run_linter -e "$go" tool shfmt --binary-next-line -d -p -s \
	./scripts/hooks/* \
	./scripts/install.sh \
	./scripts/make/*.sh \
	./scripts/snap/*.sh \
	./snap/local/*.sh \
	;

shellcheck -e 'SC2250,SC2310,SC3040' -f 'gcc' -o 'all' -x -- \
	./scripts/hooks/* \
	./scripts/install.sh \
	./scripts/make/*.sh \
	./scripts/snap/*.sh \
	./snap/local/*.sh \
	;
