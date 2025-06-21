#!/bin/bash
set -euo pipefail

# CONFIG
REPO="toxdes/glesha"
VERSION=$(cat version.txt)
TAG="v$VERSION"
GH_API="https://api.github.com/repos/$REPO"
GH_UPLOAD="https://uploads.github.com/repos/$REPO/releases"
LD_FLAGS="-X 'glesha/cmd/version_cmd.version=$(cat version.txt)' -X 'glesha/cmd/version_cmd.commitHash=$(git rev-parse --short HEAD)'"
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

		GOOS=$os GOARCH=$arch CGO_ENABLED=0 go build -ldflags="-s -w $LD_FLAGS" -o "$output"

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

mkdir -p dist/tmp
for file in dist/*; do
  filename=$(basename "$file")
  [[ "$filename" == tmp ]] && continue
  [[ "$filename" == *.zip || "$filename" == *.tar.gz ]] && continue

  dirname="${filename%.*}"  # remove extension

  mkdir -p dist/tmp/"$dirname"

  if [[ "$filename" == *.exe ]]; then
    cp "$file" dist/tmp/"$dirname"/glesha.exe
    (cd dist/tmp && zip -r "../${dirname}.zip" "$dirname")
    arc_path="dist/${dirname}.zip"
  else
    cp "$file" dist/tmp/"$dirname"/glesha
    (cd dist/tmp && tar -czf "../${dirname}.tar.gz" "$dirname")
    arc_path="dist/${dirname}.tar.gz"
  fi

  echo "Uploading $(basename "$arc_path")..."
  curl -s -X POST -H "Authorization: token $GH_TOKEN" \
       -H "Content-Type: application/octet-stream" \
       --data-binary @"$arc_path" \
       "$GH_UPLOAD/$release_id/assets?name=$(basename "$arc_path")&label=$(basename "$arc_path")"

  rm -rf dist/tmp/*
done

rm -r dist/tmp


echo "Release $TAG created and binaries uploaded successfully."
