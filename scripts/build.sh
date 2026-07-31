#!/bin/sh
set -eu

mkdir -p bin
exec go build -trimpath -o bin/herdr-source-control ./cmd/herdr-source-control
