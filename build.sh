#!/usr/bin/env bash
printf "[BUILD]\n"
set -xe
mkdir -p "./build"
go mod tidy
LD_FLAGS="-X 'glesha/cmd.version=$(cat version.txt)'"
go build -o ./build/glesha -ldflags "$LD_FLAGS"