#!/bin/bash

# Just happy path
curl -s -f -H "foo: test" -H "baz: shady" http://envoy-proxy:8888/twoheader

if [ $? -ne 0 ]; then
	exit 1
fi
