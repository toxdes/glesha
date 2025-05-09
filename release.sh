#!/bin/bash
set -euo pipefail

# CONFIG
REPO="toxdes/glesha"
VERSION=$(cat version.txt)
TAG="v$VERSION"
GH_API="https://api.github.com/repos/$REPO"
GH_UPLOAD="https://uploads.github.com/repos/$REPO/releases"

BIN_NAME="glesha"

# BUILD
mkdir -p dist
echo "Building $BIN_NAME for multiple platforms..."

targets=(
  "linux amd64"
  "darwin amd64"
  "windows amd64"
)

for target in "${targets[@]}"; do
  read os arch <<< "$target"
  output="dist/${BIN_NAME}-${os}-${arch}"
  [[ "$os" == "windows" ]] && output+=".exe"

  echo "Building $output..."

  GOOS=$os GOARCH=$arch CGO_ENABLED=0 go build -ldflags="-s -w" -o "$output"

  [[ "$os" == "linux" ]] && strip "$output"

  chmod +x "$output"
done

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

# UPLOAD ASSETS
for file in dist/*; do
  filename=$(basename "$file")
  echo "Uploading $filename..."

  curl -s -X POST -H "Authorization: token $GH_TOKEN" \
    -H "Content-Type: application/octet-stream" \
    --data-binary @"$file" \
    "$GH_UPLOAD/$release_id/assets?name=$filename" > /dev/null
done

echo "Release $TAG created and binaries uploaded successfully."