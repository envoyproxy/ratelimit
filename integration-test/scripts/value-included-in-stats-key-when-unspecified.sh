#!/bin/bash

#
# descriptor: (unspec: *)
# Has rate limit quota 2 req / min
# detailed_metric is true
#

response=$(curl -f -s -H "unspec: unspecified_value" http://envoy-proxy:8888/unspec)
response=$(curl -f -s -H "unspec: unspecified_value" http://envoy-proxy:8888/unspec)

# This should be successful
if [ $? -ne 0 ]; then
	echo "These should not be rate limited"
	exit 1
fi

# This one should be ratelimited
response=$(curl -f -s -H "unspec: unspecified_value" http://envoy-proxy:8888/unspec)

if [ $? -eq 0 ]; then
	echo "This should be a ratelimited call"
	exit 1
fi

# Sleep a bit to allow the stats to be propagated
sleep 2

# Extract the metric for the unspecified value, which shoulb be there due to the "detailed_metric"
stats=$(curl -f -s statsd:9102/metrics | grep -e ratelimit_service_rate_limit_over_limit | grep unspec_unspecified_value | cut -d} -f2 | sed 's/ //g')

echo "Length: ${#stats}"
echo "${stats}"

if [ "${stats}" != "1" ]; then
	echo "Overlimit should be 1"
	exit 1
fi
