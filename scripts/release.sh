#!/usr/bin/env bash
# Release vX.Y.Z: snapshot dev-local onto main, wait for CI, tag, wait for the release, check it.
# GitHub access comes from GH_TOKEN, for example:
#   agv run --env GH_TOKEN='{{GITHUB}}' -- scripts/release.sh v0.1.2 [--dry-run]
# --dry-run runs every check and the tests, then stops before pushing anything.
set -euo pipefail

die() { echo "release: $*" >&2; exit 1; }
step() { echo "==> $*"; }

V=${1:-}
DRY=${2:-}
[[ $V =~ ^v([0-9]+\.[0-9]+\.[0-9]+)$ ]] || die "usage: scripts/release.sh vX.Y.Z [--dry-run]"
NUM=${BASH_REMATCH[1]}
[[ -z $DRY || $DRY == --dry-run ]] || die "unknown option: $DRY"

cd "$(git rev-parse --show-toplevel)"
for cmd in git gh go; do command -v "$cmd" >/dev/null || die "$cmd is not on PATH"; done

step "checks"
[[ $(git branch --show-current) == dev-local ]] || die "run it on dev-local"
[[ -z $(git status --porcelain) ]] || die "the working tree is not clean"
grep -q "^## \[${NUM//./\\.}\]" CHANGELOG.md || die "CHANGELOG.md has no '## [$NUM]' section"
git fetch --quiet --tags origin
git rev-parse -q --verify "refs/tags/$V" >/dev/null && die "tag $V already exists"
REPO=$(gh repo view --json nameWithOwner --jq .nameWithOwner)

step "tests"
[[ -z $(gofmt -l .) ]] || die "gofmt reports unformatted files"
go vet ./...
go test -race ./...

PARENT=()
if git rev-parse -q --verify refs/remotes/origin/main >/dev/null; then
	[[ $(git rev-parse 'dev-local^{tree}') != $(git rev-parse 'origin/main^{tree}') ]] ||
		die "main already has exactly this tree; nothing to release"
	PARENT=(-p origin/main)
fi
C=$(git commit-tree 'dev-local^{tree}' "${PARENT[@]}" -m "Release $V")
step "snapshot $C (Release $V)"
if [[ $DRY == --dry-run ]]; then
	echo "dry run: stopping before any push"
	exit 0
fi

# wait_run WORKFLOW: wait for that workflow's run on commit C and require success.
wait_run() {
	local id="" status conclusion
	for _ in $(seq 60); do
		id=$(gh run list -R "$REPO" --workflow "$1" --commit "$C" --limit 1 --json databaseId --jq '.[0].databaseId // empty')
		[[ -n $id ]] && break
		sleep 5
	done
	[[ -n $id ]] || die "no $1 run started for $C"
	for _ in $(seq 120); do
		status=$(gh run view "$id" -R "$REPO" --json status --jq .status)
		[[ $status == completed ]] && break
		sleep 15
	done
	conclusion=$(gh run view "$id" -R "$REPO" --json conclusion --jq .conclusion)
	[[ $conclusion == success ]] || die "$1 run $id ended with '$conclusion'"
}

step "push main"
git push --quiet origin "$C:refs/heads/main"
git branch -f main "$C"
step "wait for CI"
wait_run ci.yml

step "tag $V"
git tag -a "$V" "$C" -m "agv $V"
git push --quiet origin "$V"
step "wait for the release"
wait_run release.yml

step "check the published binary"
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
gh release download "$V" -R "$REPO" -D "$TMP" -p "agv-$OS-$ARCH" -p "agv-$OS-$ARCH.sha256"
(cd "$TMP" && shasum -a 256 -c "agv-$OS-$ARCH.sha256")
chmod +x "$TMP/agv-$OS-$ARCH"
[[ $("$TMP/agv-$OS-$ARCH" --version) == "agv $V" ]] || die "the published binary does not report $V"
echo "released $V: https://github.com/$REPO/releases/tag/$V"
