#!/usr/bin/env bash
printf "[RUN]\n"
set -xe
./build/glesha --input=~/pro/scripts --config=./config-sample.json --provider aws
