#!/bin/bash

function assert_ok() {
	if [ $? -ne 0 ]; then
		echo "Rate limited the request, but should not have"
		exit 1
	fi
}

function assert_limited() {
	if [ $? -eq 0 ]; then
		echo "Should have rate limited the request, but it was not"
		exit 1
	fi
}

#
# Verify that replaces increases the limit.
#
# descriptor: (foo: *), (bar: banned)
# name: banned_limit
# quota: 0 / min
#
# descriptor: (source_cluster: proxy), (destination_cluster: override)
# replaces: banned_limit
# quota: 2 / min
#

response=$(curl -f -s -H "foo: bkthomps" -H "bar: banned" http://envoy-proxy:8888/twoheader)
assert_limited

response=$(curl -f -s -H "foo: bkthomps" -H "bar: banned" -H "source_cluster: proxy" -H "destination_cluster: mock" http://envoy-proxy:8888/fourheader)
assert_limited

response=$(curl -f -s -H "foo: bkthomps" -H "bar: banned" -H "source_cluster: proxy" -H "destination_cluster: override" http://envoy-proxy:8888/fourheader)
assert_ok
response=$(curl -f -s -H "foo: bkthomps" -H "bar: banned" -H "source_cluster: proxy" -H "destination_cluster: override" http://envoy-proxy:8888/fourheader)
assert_ok
response=$(curl -f -s -H "foo: bkthomps" -H "bar: banned" -H "source_cluster: proxy" -H "destination_cluster: override" http://envoy-proxy:8888/fourheader)
assert_limited

#
# Verify that replaces doesn't affect the original limit.
#
# descriptor: (foo: *), (bar: bkthomps)
# name: bkthomps
# quota: 1 / min
#
# descriptor: (source_cluster: proxy), (destination_cluster: bkthomps)
# replaces: bkthomps
# quota: 2 / min
#

response=$(curl -f -s -H "foo: my_foo" -H "bar: bkthomps" -H "source_cluster: proxy" -H "destination_cluster: bkthomps" http://envoy-proxy:8888/fourheader)
assert_ok
response=$(curl -f -s -H "foo: my_foo" -H "bar: bkthomps" -H "source_cluster: proxy" -H "destination_cluster: bkthomps" http://envoy-proxy:8888/fourheader)
assert_ok
response=$(curl -f -s -H "foo: my_foo" -H "bar: bkthomps" -H "source_cluster: proxy" -H "destination_cluster: bkthomps" http://envoy-proxy:8888/fourheader)
assert_limited

response=$(curl -f -s -H "foo: my_foo" -H "bar: bkthomps" -H "source_cluster: proxy" -H "destination_cluster: not_bkthomps" http://envoy-proxy:8888/fourheader)
assert_ok
response=$(curl -f -s -H "foo: my_foo" -H "bar: bkthomps" -H "source_cluster: proxy" -H "destination_cluster: not_bkthomps" http://envoy-proxy:8888/fourheader)
assert_limited

response=$(curl -f -s -H "foo: my_foo" -H "bar: bkthomps" -H "source_cluster: proxy" -H "destination_cluster: bkthomps" http://envoy-proxy:8888/fourheader)
assert_limited

#
# Verify that replaces can replace multiple descriptors.
#
# descriptor: (foo: *), (bar: bkthomps)
# name: bkthomps
# quota: 1 / min
#
# descriptor: (source_cluster: proxy), (destination_cluster: fake)
# name: fake_name
# quota: 2 / min
#
# descriptor: (category: account)
# replaces: bkthomps, fake_name
# quota: 4 / min
#

response=$(curl -f -s -H "foo: my_foo" -H "bar: bkthomps" -H "source_cluster: proxy" -H "destination_cluster: fake" -H "category: account" http://envoy-proxy:8888/fiveheader)
assert_ok
response=$(curl -f -s -H "foo: my_foo" -H "bar: bkthomps" -H "source_cluster: proxy" -H "destination_cluster: fake" -H "category: account" http://envoy-proxy:8888/fiveheader)
assert_ok
response=$(curl -f -s -H "foo: my_foo" -H "bar: bkthomps" -H "source_cluster: proxy" -H "destination_cluster: fake" -H "category: account" http://envoy-proxy:8888/fiveheader)
assert_ok
response=$(curl -f -s -H "foo: my_foo" -H "bar: bkthomps" -H "source_cluster: proxy" -H "destination_cluster: fake" -H "category: account" http://envoy-proxy:8888/fiveheader)
assert_ok
response=$(curl -f -s -H "foo: my_foo" -H "bar: bkthomps" -H "source_cluster: proxy" -H "destination_cluster: fake" -H "category: account" http://envoy-proxy:8888/fiveheader)
assert_limited

response=$(curl -f -s -H "foo: foo_2" -H "bar: bkthomps" http://envoy-proxy:8888/twoheader)
assert_ok
response=$(curl -f -s -H "foo: foo_2" -H "bar: bkthomps" http://envoy-proxy:8888/twoheader)
assert_limited

response=$(curl -f -s -H "foo: foo_3" -H "bar: bar_3" -H "source_cluster: proxy" -H "destination_cluster: fake" http://envoy-proxy:8888/fourheader)
assert_ok
response=$(curl -f -s -H "foo: foo_3" -H "bar: bar_3" -H "source_cluster: proxy" -H "destination_cluster: fake" http://envoy-proxy:8888/fourheader)
assert_ok
response=$(curl -f -s -H "foo: foo_3" -H "bar: bar_3" -H "source_cluster: proxy" -H "destination_cluster: fake" http://envoy-proxy:8888/fourheader)
assert_limited

response=$(curl -f -s -H "foo: my_foo" -H "bar: bkthomps" -H "source_cluster: proxy" -H "destination_cluster: fake" -H "category: account" http://envoy-proxy:8888/fiveheader)
assert_limited
