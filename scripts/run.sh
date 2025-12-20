#!/usr/bin/env bash
printf "[RUN]\n"
set -xe

# DEBUG_FLAG="-L debug"
DEBUG_FLAG=""
dd if=/dev/urandom of=output.dat  bs=100M  count=2
./build/glesha add $DEBUG_FLAG -p aws -a targz -c ../glesha-secrets/config.json output.dat
./build/glesha run $DEBUG_FLAG -j 4 1
rm output.dat
# ./build/glesha add -p aws -L debug ~/pro/scripts
# ./build/glesha -i ~/pro/scripts provider":"aws",-p aws -c ../glesha-secrets/config.json --assume-yes
# ./build/glesha help config
