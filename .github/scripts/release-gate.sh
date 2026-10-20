#!/bin/sh
# The release gate (GOVERNANCE.md, "Before a release is tagged"): a tag is
# published only when the checks its kind of release needs passed on the
# exact commit it names.
#
#   every tag:                 ci succeeded on the commit
#   a release (no -rc.N):      and a nightly run succeeded on the commit
#   a minor or major (x.y.0):  and a nightly run with a chaos soak of 72h or
#                              more succeeded on the commit
#
# A nightly run on the commit: `gh workflow run nightly.yml --ref <tag>`
# (add -f chaos=72h -f runner=<self-hosted label> for a minor release).
#
#   GH_TOKEN=… REPO=admirable-oss/hive TAG=v1.1.0 SHA=<commit> release-gate.sh
set -eu
: "${REPO:?}" "${TAG:?}" "${SHA:?}"

fail() { echo "::error::release gate: $1" >&2; exit 1; }

# runs <workflow file>: the workflow's completed, successful runs on SHA, one
# display title per line.
runs() {
  gh api --paginate "repos/${REPO}/actions/workflows/$1/runs?head_sha=${SHA}&status=success&per_page=100" \
    --jq '.workflow_runs[] | select(.conclusion == "success") | .display_title'
}

version=${TAG#v}
core=${version%%-*}
prerelease=false
[ "$core" != "$version" ] && prerelease=true
patch=${core##*.}

[ -n "$(runs ci.yml)" ] || fail "ci has not succeeded on ${SHA} (the commit ${TAG} names)"
echo "ci succeeded on ${SHA}"
if [ "$prerelease" = true ]; then
  echo "${TAG} is a release candidate: ci is enough"
  exit 0
fi

nightly=$(runs nightly.yml)
[ -n "$nightly" ] || fail "no nightly run has succeeded on ${SHA}; run one: gh workflow run nightly.yml --ref ${TAG}"
echo "nightly succeeded on ${SHA}"
if [ "$patch" != 0 ]; then
  exit 0
fi

# Minor and major releases: the 72-hour chaos gate. nightly.yml names its
# runs "nightly (chaos <duration>)".
longest=$(printf '%s\n' "$nightly" | sed -n 's/.*chaos \([0-9][0-9]*\)h.*/\1/p' | sort -n | tail -1)
[ "${longest:-0}" -ge 72 ] || fail "${TAG} is a minor or major release and needs a successful nightly run with a chaos soak of 72h or more on ${SHA} (longest: ${longest:-none}); run one on a self-hosted runner: gh workflow run nightly.yml --ref ${TAG} -f chaos=72h -f runner=<label>"
echo "chaos soak of ${longest}h succeeded on ${SHA}"
