#!/bin/bash
set -e

export GITHUB_TOKEN="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"

./build.local.sh
rm -rf dist

git add .
git commit -m "chore: release $new_tag"
git push origin main

latest_tag=$(git tag --list 'v*' | sort -V | tail -n 1)
# If no tag yet, start at v0.0.0
if [ -z "$latest_tag" ]; then
  new_tag="v0.0.1"
else
  IFS='.' read -r major minor patch <<<"${latest_tag#v}"
  patch=$((patch + 1))
  new_tag="v${major}.${minor}.${patch}"
fi
echo "$new_tag"
git tag "$new_tag"
git push origin "$new_tag"

./goreleaser release