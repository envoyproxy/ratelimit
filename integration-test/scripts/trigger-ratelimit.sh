#!/bin/bash

#
# descriptor: (foo: *), (baz: not-so-shady)
# Has rate limit quota 3 req / min
#

response=$(curl -f -s -H "foo: pelle" -H "baz: not-so-shady" http://envoy-proxy:8888/twoheader)
response=$(curl -f -s -H "foo: pelle" -H "baz: not-so-shady" http://envoy-proxy:8888/twoheader)
response=$(curl -f -s -H "foo: pelle" -H "baz: not-so-shady" http://envoy-proxy:8888/twoheader)

if [ $? -ne 0 ]; then
	echo "Rate limit should not trigger yet"
	exit 1
fi

response=$(curl -f -s -H "foo: pelle" -H "baz: not-so-shady" http://envoy-proxy:8888/twoheader)

if [ $? -eq 0 ]; then
	echo "Rate limiting should fail the request"
	exit 1
fi

response=$(curl -i -s -H "foo: pelle" -H "baz: not-so-shady" http://envoy-proxy:8888/twoheader | grep "Too Many Requests")
if [ $? -ne 0 ]; then
	echo "This should trigger a ratelimit"
	exit 1
fi
