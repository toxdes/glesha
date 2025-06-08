#!/usr/bin/env bash
printf "[RUN]\n"
set -xe
# ./build/glesha  --provider aws -c ../glesha-secrets/config.json -i ~/pro/scripts --verbose
# ./build/glesha -p aws -i ~/pro/scripts
# ./build/glesha -i ~/pro/scripts -p aws -c ../glesha-secrets/config.json --assume-yes
./build/glesha --help
