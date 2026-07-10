#!/usr/bin/env bash
# Mark every published release except the current "latest" (the highest-semver,
# non-prerelease release, as computed by GitHub) as superseded, by prepending a
# marker-delimited banner that points at the latest release.
#
# Idempotent: the banner block is stripped and, for non-latest releases,
# re-added on each run, so re-running — or cutting a newer release — just
# re-points every predecessor at the new latest, and clears the banner from the
# release that has become latest. A release edited to be unchanged is skipped.
#
# Run automatically by .github/workflows/release.yml after goreleaser publishes,
# and safe to run by hand: REPO=owner/name bash .github/scripts/supersede-releases.sh
set -euo pipefail

REPO="${REPO:?set REPO=owner/name}"

latest_tag=$(gh release view --repo "$REPO" --json tagName --jq .tagName)
latest_url=$(gh release view "$latest_tag" --repo "$REPO" --json url --jq .url)

banner="<!-- superseded -->
> [!WARNING]
> **Superseded.** A newer release is available: **[${latest_tag}](${latest_url})**. Use the latest release.
<!-- /superseded -->
"

for tag in $(gh release list --repo "$REPO" --limit 200 --json tagName --jq '.[].tagName'); do
  body=$(gh release view "$tag" --repo "$REPO" --json body --jq .body)
  # Drop any existing banner block (between the markers, plus trailing blank lines).
  clean=$(printf '%s' "$body" | perl -0pe 's/<!-- superseded -->.*?<!-- \/superseded -->\n*//s')
  if [ "$tag" = "$latest_tag" ]; then
    new="$clean"
  else
    new="${banner}
${clean}"
  fi
  if [ "$new" != "$body" ]; then
    printf '%s' "$new" | gh release edit "$tag" --repo "$REPO" --notes-file -
    echo "updated: $tag"
  else
    echo "unchanged: $tag"
  fi
done
