#!/usr/bin/env bash
printf "[RUN]\n"
set -xe

DEBUG_FLAG="-L info"
# dd if=/dev/urandom of=output.dat  bs=100M  count=2
./build/glesha add $DEBUG_FLAG -p aws -a targz -c ../glesha-secrets/config.json ~/pro/dotfiles
./build/glesha run $DEBUG_FLAG -j 4 1
# rm output.dat
# ./build/glesha add -p aws -L debug ~/pro/scripts
# ./build/glesha help config
# ./build/glesha tui
