#!/bin/bash
set -euo pipefail

# CONFIG
REPO="toxdes/glesha"
VERSION=$(cat version.txt)
TAG="v$VERSION"
GH_API="https://api.github.com/repos/$REPO"
GH_UPLOAD="https://uploads.github.com/repos/$REPO/releases"
# RELEASE
echo "Creating GitHub release $TAG..."

release_response=$(curl -X POST -H "Authorization: token $GH_TOKEN" \
												-d "{\"tag_name\": \"$TAG\", \"name\": \"$TAG\", \"draft\": false, \"prerelease\": false}" \
												"$GH_API/releases")

release_id=$(echo "$release_response" | grep -m 1 '"id":' | grep -o '[0-9]\+')

if [ -z "$release_id" ]; then
		echo "Failed to create release:"
		echo "$release_response"
		exit 1
fi

for file in dist/*; do
  filename=$(basename "$file")
  echo ""
  echo "Uploading to Github release: $filename..."
  curl -s -X POST -H "Authorization: token $GH_TOKEN" \
       -H "Content-Type: application/octet-stream" \
       --data-binary @"$file" \
       "$GH_UPLOAD/$release_id/assets?name=$filename&label=$filename"
done

echo "Release $TAG created and binaries uploaded successfully."
