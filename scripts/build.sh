#!/usr/bin/env bash
printf "[BUILD]\n"
set -xe
mkdir -p "./build"
go mod tidy
LD_FLAGS="-X 'glesha/cmd/version_cmd.version=$(cat version.txt)' -X 'glesha/cmd/version_cmd.commitHash=$(git rev-parse --short HEAD)' -X 'glesha/logger.printCallerLocation=true'"
go build -o ./build/glesha -ldflags "$LD_FLAGS"
