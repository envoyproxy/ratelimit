#!/bin/bash

#
# request to /tokenquota will produce the (service: service_1), (service: service_2) descriptor
# with 0 addend on request path and (service: service_2) descriptor with addend 1 on response path.
# service_2 has 2 req/min so 2 requests sahould be allowed
#

response=$(curl -f -s -H "request-no: 1" http://envoy-proxy:8888/tokenquota)
response=$(curl -f -s -H "request-no: 2" http://envoy-proxy:8888/tokenquota)
response=$(curl -f -s -H "request-no: 3" http://envoy-proxy:8888/tokenquota)

if [ $? -ne 0 ]; then
	echo "Quota limit should not trigger yet"
	exit 1
fi

# Quota is debited from service_2 bucket on the response path sop only the 4thrd request should be rejected
response=$(curl -f -s -H "request-no: 4" http://envoy-proxy:8888/tokenquota)

if [ $? -eq 0 ]; then
	echo "Quota limiting should fail the request"
	exit 1
fi

echo "Waiting 1 minute for quota buckets to be refreshed"
sleep 60

response=$(curl -i -s -H "request-no: 5" http://envoy-proxy:8888/tokenquota)
if [ $? -ne 0 ]; then
	echo "Quota bucket should be refreshed"
	exit 1
fi
