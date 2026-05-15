#!/bin/bash

#
# request to /multiservice-tokenquota will produce the (service: service_1), (service: service_2) descriptor
# with 0 addend on request path and on response path descriptor containing the name of service which served the request with addend 1.
# RL service responds with metadata containing the service name with available quota.
# service_1 has 1 req/min, service_2 has 2 req/min. Since quota is depbited on response path a total of 2 requests/minute for service_1
# and 3 requests/minute for service_2 is allowed.
#

response=$(curl -f -s -H "request-no: 1" http://envoy-proxy:8888/multiservice-tokenquota | grep "from Service 1")
response=$(curl -f -s -H "request-no: 2" http://envoy-proxy:8888/multiservice-tokenquota | grep "from Service 1")
# service_1 is out of quota and service_2 should now be used
response=$(curl -f -s -H "request-no: 3" http://envoy-proxy:8888/multiservice-tokenquota | grep "from Service 2")
response=$(curl -f -s -H "request-no: 4" http://envoy-proxy:8888/multiservice-tokenquota | grep "from Service 2")
response=$(curl -f -s -H "request-no: 5" http://envoy-proxy:8888/multiservice-tokenquota | grep "from Service 2")

if [ $? -ne 0 ]; then
	echo "Quota limit should not trigger yet"
	exit 1
fi

# Quota is debited from service_2 bucket on the response path so only the 5th request should be rejected
response=$(curl -f -s -H "request-no: 6" http://envoy-proxy:8888/multiservice-tokenquota | grep "Hello World from Service")

if [ $? -eq 0 ]; then
	echo "Quota limiting should fail the request"
	exit 1
fi

echo "Waiting 1 minute for quota buckets to be refreshed"
sleep 60

response=$(curl -f -s -H "request-no: 7" http://envoy-proxy:8888/multiservice-tokenquota | grep "from Service 1")
if [ $? -ne 0 ]; then
	echo "Quota bucket should be refreshed and service_1 is used"
	exit 1
fi
