#!/usr/bin/env bash
printf "\n[BUILD]\n"
set -xe
mkdir -p "./build"
go mod tidy
go build -o ./build/glesha