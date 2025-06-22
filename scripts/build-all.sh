#!/bin/bash
REPO="toxdes/glesha"
VERSION=$(cat version.txt)
TAG="v$VERSION"
COMMIT_HASH=$(git rev-parse --short HEAD)

docker buildx build \
  --platform=linux/amd64,linux/386,linux/arm64,linux/arm/v7,\
darwin/amd64,darwin/arm64,\
windows/amd64,windows/386,windows/arm64,\
freebsd/amd64,freebsd/arm64\
  --build-arg VERSION="$VERSION" \
  --build-arg COMMIT_HASH="$COMMIT_HASH" \
  --output=type=local,dest=./build \
  -f Dockerfile .
