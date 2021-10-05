#!/bin/bash

#
# descriptor: (foo: *), (baz: shady)
# Has rate limit quota 3 req / min
# shadow_mode is true
#

response=$(curl -f -s -H "foo: pelle" -H "baz: shady" http://envoy-proxy:8888/twoheader)
response=$(curl -f -s -H "foo: pelle" -H "baz: shady" http://envoy-proxy:8888/twoheader)
response=$(curl -f -s -H "foo: pelle" -H "baz: shady" http://envoy-proxy:8888/twoheader)
response=$(curl -f -s -H "foo: pelle" -H "baz: shady" http://envoy-proxy:8888/twoheader)
response=$(curl -f -s -H "foo: pelle" -H "baz: shady" http://envoy-proxy:8888/twoheader)

if [ $? -ne 0 ]; then
	echo "Shadow Mode key should not trigger an error, even if we have exceeded the quota"
	exit 1
fi

remaining=$(curl -i -s -H "foo: pelle" -H "baz: shady" http://envoy-proxy:8888/twoheader | grep x-ratelimit-remaining | cut -d: -f2 | cut -d: -f2 | sed 's/ //g')

if [ $remaining == "0" ]; then
	echo "Remaining should be zero"
	exit 1
fi
