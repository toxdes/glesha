#!/usr/bin/env bash
printf "[RUN]\n"
set -xe
./build/glesha --input=. --config=./config-sample.json --verbose
