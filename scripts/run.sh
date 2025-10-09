#!/usr/bin/env bash
printf "[RUN]\n"
set -xe

DEBUG_FLAG="-L info"
# DEBUG_FLAG=""

./build/glesha add $DEBUG_FLAG -p aws -a targz -c ../glesha-secrets/config.json ~/pro/scripts
./build/glesha run $DEBUG_FLAG 1
# ./build/glesha add -p aws -L debug ~/pro/scripts
# ./build/glesha -i ~/pro/scripts provider":"aws",-p aws -c ../glesha-secrets/config.json --assume-yes
# ./build/glesha help config
