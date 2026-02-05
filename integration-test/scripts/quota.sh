#!/bin/bash

#
# request to /quota will produce the (service: service_1), (service: service_2) descriptor
# service_1 has 1 req/min and service_2 has 2 req/min
#

response=$(curl -f -s http://envoy-proxy:8888/quota)
response=$(curl -f -s http://envoy-proxy:8888/quota)

if [ $? -ne 0 ]; then
	echo "Quota limit should not trigger yet"
	exit 1
fi

# Quota is debited from all matching buckets and 3rd request should be rejected
response=$(curl -f -s http://envoy-proxy:8888/quota | grep "Too Many Requests")

if [ $? -eq 0 ]; then
	echo "Quota limiting should fail the request"
	exit 1
fi

echo "Wating 1 minute for quota buckets to be refreshed"
sleep 60

response=$(curl -i -s http://envoy-proxy:8888/quota)
if [ $? -ne 0 ]; then
	echo "Quota bucket should be refereshed"
	exit 1
fi
