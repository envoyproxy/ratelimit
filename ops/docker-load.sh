#!/usr/bin/env bash

echo "loading docker images for version: [$CIRCLE_TAG]"
docker load -i ratelimit.tar
