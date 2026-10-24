#!/bin/sh
# attach.sh uploads files to the release of the pushed tag ($GITHUB_REF_NAME), making the release
# first if no workflow has yet: its notes the previous release's with the version swapped, to edit.
set -e
tag=$GITHUB_REF_NAME repo=$GITHUB_REPOSITORY
if ! gh release view "$tag" -R "$repo" >/dev/null 2>&1; then
	prev=$(gh release list -R "$repo" --exclude-drafts --limit 1 --json tagName -q '.[0].tagName')
	old=$(printf %s "${prev#v}" | sed 's/[.]/\\./g')
	gh release view "$prev" -R "$repo" --json body -q .body | sed "s/$old/${tag#v}/g" > "$RUNNER_TEMP/notes.md"
	pre=
	case $tag in *-*) pre=--prerelease ;; esac # a test build (v1.0.0-rc.1): not offered as the latest
	# two workflows can get here at once: the second's create fails, the release being there
	gh release create "$tag" -R "$repo" --verify-tag $pre --title "Connaio $tag" --notes-file "$RUNNER_TEMP/notes.md" ||
		gh release view "$tag" -R "$repo" >/dev/null
fi
gh release upload "$tag" "$@" --clobber -R "$repo"
