<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->

- [Overview](#overview)
- [Docker Image](#docker-image)
- [Supported Envoy APIs](#supported-envoy-apis)
  - [API Deprecation History](#api-deprecation-history)
- [Building and Testing](#building-and-testing)
  - [Docker-compose setup](#docker-compose-setup)
  - [Full test environment](#full-test-environment)
  - [Self-contained end-to-end integration test](#self-contained-end-to-end-integration-test)
- [Configuration](#configuration)
  - [The configuration format](#the-configuration-format)
    - [Definitions](#definitions)
    - [Descriptor list definition](#descriptor-list-definition)
    - [Rate limit definition](#rate-limit-definition)
    - [Replaces](#replaces)
    - [ShadowMode](#shadowmode)
    - [Examples](#examples)
      - [Example 1](#example-1)
      - [Example 2](#example-2)
      - [Example 3](#example-3)
      - [Example 4](#example-4)
      - [Example 5](#example-5)
      - [Example 6](#example-6)
      - [Example 7](#example-7)
  - [Loading Configuration](#loading-configuration)
  - [Log Format](#log-format)
  - [GRPC Keepalive](#grpc-keepalive)
- [Request Fields](#request-fields)
- [GRPC Client](#grpc-client)
  - [Commandline flags](#commandline-flags)
- [Global ShadowMode](#global-shadowmode)
  - [Configuration](#configuration-1)
  - [Statistics](#statistics)
- [Statistics](#statistics-1)
  - [Statistics options](#statistics-options)
- [HTTP Port](#http-port)
  - [/json endpoint](#json-endpoint)
- [Debug Port](#debug-port)
- [Local Cache](#local-cache)
- [Redis](#redis)
  - [Redis type](#redis-type)
  - [Pipelining](#pipelining)
  - [One Redis Instance](#one-redis-instance)
  - [Two Redis Instances](#two-redis-instances)
  - [Health Checking for Redis Active Connection](#health-checking-for-redis-active-connection)
- [Memcache](#memcache)
- [Custom headers](#custom-headers)
- [Tracing](#tracing)
- [mTLS](#mtls)
- [Contact](#contact)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Overview

The rate limit service is a Go/gRPC service designed to enable generic rate limit scenarios from different types of
applications. Applications request a rate limit decision based on a domain and a set of descriptors. The service
reads the configuration from disk via [runtime](https://github.com/lyft/goruntime), composes a cache key, and talks to the Redis cache. A
decision is then returned to the caller.

# Docker Image

For every main commit, an image is pushed to [Dockerhub](https://hub.docker.com/r/envoyproxy/ratelimit/tags?page=1&ordering=last_updated). There is currently no versioning (post v1.4.0) and tags are based on commit sha.

# Supported Envoy APIs

[v3 rls.proto](https://github.com/envoyproxy/data-plane-api/blob/master/envoy/service/ratelimit/v3/rls.proto) is currently supported.
Support for [v2 rls proto](https://github.com/envoyproxy/data-plane-api/blob/master/envoy/service/ratelimit/v2/rls.proto) is now deprecated.

## API Deprecation History

1. `v1.0.0` tagged on commit `0ded92a2af8261d43096eba4132e45b99a3b8b14`. Ratelimit has been in production use at Lyft for over 2 years.
2. `v1.1.0` introduces the data-plane-api proto and initiates the deprecation of the legacy [ratelimit.proto](https://github.com/lyft/ratelimit/blob/0ded92a2af8261d43096eba4132e45b99a3b8b14/proto/ratelimit/ratelimit.proto).
3. `e91321b` [commit](https://github.com/envoyproxy/ratelimit/commit/e91321b10f1ad7691d0348e880bd75d0fca05758) deleted support for the legacy [ratelimit.proto](https://github.com/envoyproxy/ratelimit/blob/0ded92a2af8261d43096eba4132e45b99a3b8b14/proto/ratelimit/ratelimit.proto).
   The current version of ratelimit protocol is changed to [v3 rls.proto](https://github.com/envoyproxy/data-plane-api/blob/master/envoy/service/ratelimit/v3/rls.proto)
   while [v2 rls.proto](https://github.com/envoyproxy/data-plane-api/blob/master/envoy/service/ratelimit/v3/rls.proto) is still supported
   as a legacy protocol.
4. `4bb32826` deleted support for legacy [v2 rls.proto](https://github.com/envoyproxy/data-plane-api/blob/master/envoy/service/ratelimit/v3/rls.proto)

# Building and Testing

- Install Redis-server.
- Make sure go is setup correctly and checkout rate limit service into your go path. More information about installing
  go [here](https://golang.org/doc/install).
- In order to run the integration tests using a local Redis server please run two Redis-server instances: one on port `6379` and another on port `6380`
  ```bash
  redis-server --port 6379 &
  redis-server --port 6380 &
  ```
- To setup for the first time (only done once):
  ```bash
  make bootstrap
  ```
- To compile:

  ```bash
  make compile
  ```

  Ensure you set the correct platform if running OSX host with a linux container e.g.

  ```bash
  GOOS=linux make compile
  ```

- To compile and run tests:
  ```bash
  make tests
  ```
- To run the server locally using some sensible default settings you can do this (this will setup the server to read the configuration files from the path you specify):
  ```bash
  USE_STATSD=false LOG_LEVEL=debug REDIS_SOCKET_TYPE=tcp REDIS_URL=localhost:6379 RUNTIME_ROOT=/home/user/src/runtime/data RUNTIME_SUBDIRECTORY=ratelimit
  ```

## Docker-compose setup

The docker-compose setup has three containers: redis, ratelimit-build, and ratelimit. In order to run the docker-compose setup from the root of the repo, run

```bash
docker-compose up
```

The ratelimit-build container will build the ratelimit binary. Then via a shared volume the binary will be shared with the ratelimit container. This dual container setup is used in order to use a
a minimal container to run the application, rather than the heftier container used to build it.

If you want to run with [two redis instances](#two-redis-instances), you will need to modify
the docker-compose.yml file to run a second redis container, and change the environment variables
as explained in the [two redis instances](#two-redis-instances) section.

## Full test environment

To run a fully configured environment to demo Envoy based rate limiting, run:

```bash
docker-compose -f docker-compose-example.yml up --build --remove-orphans
```

This will run ratelimit, redis, prom-statsd-exporter and two Envoy containers such that you can demo rate limiting by hitting the below endpoints.

```bash
curl localhost:8888/test
curl localhost:8888/header -H "foo: foo" # Header based
curl localhost:8888/twoheader -H "foo: foo" -H "bar: bar" # Two headers
curl localhost:8888/twoheader -H "foo: foo" -H "baz: baz"  # This will be rate limited
curl localhost:8888/twoheader -H "foo: foo" -H "bar: banned" # Ban a particular header value
curl localhost:8888/twoheader -H "foo: foo" -H "baz: shady" # This will never be ratelimited since "baz" with value "shady" is in shadow_mode
curl localhost:8888/twoheader -H "foo: foo" -H "baz: not-so-shady" # This is subject to rate-limiting because the it's now in shadow_mode
```

Edit `examples/ratelimit/config/example.yaml` to test different rate limit configs. Hot reloading is enabled.

The descriptors in `example.yaml` and the actions in `examples/envoy/proxy.yaml` should give you a good idea on how to configure rate limits.

To see the metrics in the example

```bash
# The metrics for the shadow_mode keys
curl http://localhost:9102/metrics | grep -i shadow
```

## Self-contained end-to-end integration test

Integration tests are coded as bash-scripts in `integration-test/scripts`.

The test suite will spin up a docker-compose environment from `integration-test/docker-compose-integration-test.yml`

If the test suite fails it will exit with code 1.

```bash
make integration_tests
```

# Configuration

## The configuration format

The rate limit configuration file format is YAML (mainly so that comments are supported).

### Definitions

- **Domain:** A domain is a container for a set of rate limits. All domains known to the Ratelimit service must be
  globally unique. They serve as a way for different teams/projects to have rate limit configurations that don't conflict.
- **Descriptor:** A descriptor is a list of key/value pairs owned by a domain that the Ratelimit service uses to
  select the correct rate limit to use when limiting. Descriptors are case-sensitive. Examples of descriptors are:
  - ("database", "users")
  - ("message_type", "marketing"),("to_number","2061234567")
  - ("to_cluster", "service_a")
  - ("to_cluster", "service_a"),("from_cluster", "service_b")

### Descriptor list definition

Each configuration contains a top level descriptor list and potentially multiple nested lists beneath that. The format is:

```yaml
domain: <unique domain ID>
descriptors:
  - key: <rule key: required>
    value: <rule value: optional>
    rate_limit: (optional block)
      name: (optional)
      replaces: (optional)
       - name: (optional)
      unit: <see below: required>
      requests_per_unit: <see below: required>
    shadow_mode: (optional)
    descriptors: (optional block)
      - ... (nested repetition of above)
```

Each descriptor in a descriptor list must have a key. It can also optionally have a value to enable a more specific
match. The "rate_limit" block is optional and if present sets up an actual rate limit rule. See below for how the
rule is defined. If the rate limit is not present and there are no nested descriptors, then the descriptor is
effectively whitelisted. Otherwise, nested descriptors allow more complex matching and rate limiting scenarios.

### Rate limit definition

```yaml
rate_limit:
  unit: <second, minute, hour, day>
  requests_per_unit: <uint>
```

The rate limit block specifies the actual rate limit that will be used when there is a match.
Currently the service supports per second, minute, hour, and day limits. More types of limits may be added in the
future based on user demand.

### Replaces

The replaces key indicates that this descriptor will replace the configuration set by another descriptor.

If there is a rule being evaluated, and multiple descriptors can apply, the replaces descriptor will drop evaluation of
the descriptor which it is replacing.

To enable this, any descriptor which should potentially be replaced by another should have a name keyword in the
rate_limit section, and any descriptor which should potentially replace the original descriptor should have a name
keyword in its respective replaces section. Whenever limits match to both rules, only the rule which replaces the
original will take effect, and the limit of the original will not be changed after evaluation.

For example, let's say you have a bunch of endpoints and each is classified under read or write, with read having a
certain limit and write having another. Each user has a certain limit for both endpoints. However, let's say that you
want to increase a user's limit to a single read endpoint. The only option without using replaces would be to increase
their limit for the read category. The replaces keyword allows increasing the limit of a single endpoint in this case.

### ShadowMode

A shadow_mode key in a rule indicates that whatever the outcome of the evaluation of the rule, the end-result will always be "OK".

When a block is in ShadowMode all functions of the rate limiting service are executed as normal, with cache-lookup and statistics

An additional statistic is added to keep track of how many times a key with "shadow_mode" has overridden result.

There is also a Global Shadow Mode

### Examples

#### Example 1

Let's start with a simple example:

```yaml
domain: mongo_cps
descriptors:
  - key: database
    value: users
    rate_limit:
      unit: second
      requests_per_unit: 500

  - key: database
    value: default
    rate_limit:
      unit: second
      requests_per_unit: 500
```

In the configuration above
the domain is "mongo_cps" and we setup 2 different rate limits in the top level descriptor list. Each of the limits
have the same key ("database"). They have a different value ("users", and "default"), and each of them setup a 500
request per second rate limit.

#### Example 2

A slightly more complex example:

```yaml
domain: messaging
descriptors:
  # Only allow 5 marketing messages a day
  - key: message_type
    value: marketing
    descriptors:
      - key: to_number
        rate_limit:
          unit: day
          requests_per_unit: 5

  # Only allow 100 messages a day to any unique phone number
  - key: to_number
    rate_limit:
      unit: day
      requests_per_unit: 100
```

In the preceding example, the domain is "messaging" and we setup two different scenarios that illustrate more
complex functionality. First, we want to limit on marketing messages to a specific number. To enable this, we make
use of _nested descriptor lists._ The top level descriptor is ("message_type", "marketing"). However this descriptor
does not have a limit assigned so it's just a placeholder. Contained within this entry we have another descriptor list
that includes an entry with key "to_number". However, notice that no value is provided. This means that the service
will match against any value supplied for "to_number" and generate a unique limit. Thus, ("message_type", "marketing"),
("to_number", "2061111111") and ("message_type", "marketing"),("to_number", "2062222222") will each get 5 requests
per day.

The configuration also sets up another rule without a value. This one creates an overall limit for messages sent to
any particular number during a 1 day period. Thus, ("to_number", "2061111111") and ("to_number", "2062222222") both
get 100 requests per day.

When calling the rate limit service, the client can specify _multiple descriptors_ to limit on in a single call. This
limits round trips and allows limiting on aggregate rule definitions. For example, using the preceding configuration,
the client could send this complete request (in pseudo IDL):

```
RateLimitRequest:
  domain: messaging
  descriptor: ("message_type", "marketing"),("to_number", "2061111111")
  descriptor: ("to_number", "2061111111")
```

And the service will rate limit against _all_ matching rules and return an aggregate result; a logical OR of all
the individual rate limit decisions.

#### Example 3

An example to illustrate matching order.

```yaml
domain: edge_proxy_per_ip
descriptors:
  - key: remote_address
    rate_limit:
      unit: second
      requests_per_unit: 10

  # Black list IP
  - key: remote_address
    value: 50.0.0.5
    rate_limit:
      unit: second
      requests_per_unit: 0
```

In the preceding example, we setup a generic rate limit for individual IP addresses. The architecture's edge proxy can
be configured to make a rate limit service call with the descriptor `("remote_address", "50.0.0.1")` for example. This IP would
get 10 requests per second as
would any other IP. However, the configuration also contains a second configuration that explicitly defines a
value along with the same key.
If the descriptor `("remote_address", "50.0.0.5")` is received, the service
will _attempt the most specific match possible_. This means
the most specific descriptor at the same level as your request. Thus, key/value is always attempted as a match before just key.

#### Example 4

The Ratelimit service matches requests to configuration entries with the same level, i.e
same number of tuples in the request's descriptor as nested levels of descriptors
in the configuration file. For instance, the following request:

```
RateLimitRequest:
  domain: example4
  descriptor: ("key", "value"),("subkey", "subvalue")
```

Would **not** match the following configuration. Even though the first descriptor in
the request matches the 1st level descriptor in the configuration, the request has
two tuples in the descriptor.

```yaml
domain: example4
descriptors:
  - key: key
    value: value
    rate_limit:
      requests_per_unit: 300
      unit: second
```

However, it would match the following configuration:

```yaml
domain: example4
descriptors:
  - key: key
    value: value
    descriptors:
      - key: subkey
        rate_limit:
          requests_per_unit: 300
          unit: second
```

#### Example 5

We can also define unlimited rate limit descriptors:

```yaml
domain: internal
descriptors:
  - key: ldap
    rate_limit:
      unlimited: true

  - key: azure
    rate_limit:
      unit: minute
      requests_per_unit: 100
```

For an unlimited descriptor, the request will not be sent to the underlying cache (Redis/Memcached), but will be quickly returned locally by the ratelimit instance.
This can be useful for collecting statistics, or if one wants to define a descriptor that has no limit but the client wants to distinguish between such descriptor and one that does not exist.

The return value for unlimited descriptors will be an OK status code with the LimitRemaining field set to MaxUint32 value.

#### Example 6

A rule using shadow_mode is useful for soft-launching rate limiting. In this example

```
RateLimitRequest:
  domain: example6
  descriptor: ("service", "auth-service"),("user", "user-a")
```

`user-a` of the `auth-service` would not get rate-limited regardless of the rate of requests, there would however be statistics related to the breach of the configured limit of 10 req / sec.

`user-b` would be limited to 20 req / sec however.

```yaml
domain: example6
descriptors:
  - key: service
    descriptors:
      - key: user
        value: user-a
        rate_limit:
          requests_per_unit: 10
          unit: second
        shadow_mode: true
      - key: user
        value: user-b
        rate_limit:
          requests_per_unit: 20
          unit: second
```

#### Example 7

When the replaces keyword is used, that limit will replace any limit which has the name being replaced as its name, and
the original descriptor's limit will not be affected.

In the example below, the following limits will apply:

```
(key_1, value_1), (user, bkthomps): 5 / sec
(key_2, value_2), (user, bkthomps): 10 / sec
(key_1, value_1), (key_2, value_2), (user, bkthomps): 10 / sec since the (key_1, value_1), (user, bkthomps) rule was replaced and this will not affect the 5 / sec limit that would take effect with (key_2, value_2), (user, bkthomps)
```

```yaml
domain: example7
descriptors:
  - key: key_1
    value: value_1
    descriptors:
      - key: user
        value: bkthomps
        rate_limit:
          name: specific_limit
          requests_per_unit: 5
          unit: second
  - key: key_2
    value: value_2
    descriptors:
      - key: user
        value: bkthomps
        rate_limit:
          replaces:
            - name: specific_limit
          requests_per_unit: 10
          unit: second
```

## Loading Configuration

The Ratelimit service uses a library written by Lyft called [goruntime](https://github.com/lyft/goruntime) to do configuration loading. Goruntime monitors
a designated path, and watches for symlink swaps to files in the directory tree to reload configuration files.

The path to watch can be configured via the [settings](https://github.com/envoyproxy/ratelimit/blob/master/src/settings/settings.go)
package with the following environment variables:

```
RUNTIME_ROOT default:"/srv/runtime_data/current"
RUNTIME_SUBDIRECTORY
RUNTIME_IGNOREDOTFILES default:"false"
```

**Configuration files are loaded from RUNTIME_ROOT/RUNTIME_SUBDIRECTORY/config/\*.yaml**

There are two methods for triggering a configuration reload:

1. Symlink RUNTIME_ROOT to a different directory.
2. Update the contents inside `RUNTIME_ROOT/RUNTIME_SUBDIRECTORY/config/` directly.

The former is the default behavior. To use the latter method, set the `RUNTIME_WATCH_ROOT` environment variable to `false`.

The following filesystem operations on configuration files inside `RUNTIME_ROOT/RUNTIME_SUBDIRECTORY/config/` will force a reload of all config files:

- Write
- Create
- Chmod
- Remove

For more information on how runtime works you can read its [README](https://github.com/lyft/goruntime).

By default it is not possible to define multiple configuration files within `RUNTIME_SUBDIRECTORY` referencing the same domain.
To enable this behavior set `MERGE_DOMAIN_CONFIG` to `true`.

## Log Format

A centralized log collection system works better with logs in json format. JSON format avoids the need for custom parsing rules.
The Ratelimit service produces logs in a text format by default. For Example:

```
time="2020-09-10T17:22:35Z" level=debug msg="loading domain: messaging"
time="2020-09-10T17:22:35Z" level=debug msg="loading descriptor: key=messaging.message_type_marketing"
time="2020-09-10T17:22:35Z" level=debug msg="loading descriptor: key=messaging.message_type_marketing.to_number ratelimit={requests_per_unit=5, unit=DAY}"
time="2020-09-10T17:22:35Z" level=debug msg="loading descriptor: key=messaging.to_number ratelimit={requests_per_unit=100, unit=DAY}"
time="2020-09-10T17:21:55Z" level=warning msg="Listening for debug on ':6070'"
time="2020-09-10T17:21:55Z" level=warning msg="Listening for HTTP on ':8080'"
time="2020-09-10T17:21:55Z" level=debug msg="waiting for runtime update"
time="2020-09-10T17:21:55Z" level=warning msg="Listening for gRPC on ':8081'"
```

JSON Log format can be configured using the following environment variables:

```
LOG_FORMAT=json
```

Output example:

```
{"@message":"loading domain: messaging","@timestamp":"2020-09-10T17:22:44.926010192Z","level":"debug"}
{"@message":"loading descriptor: key=messaging.message_type_marketing","@timestamp":"2020-09-10T17:22:44.926019315Z","level":"debug"}
{"@message":"loading descriptor: key=messaging.message_type_marketing.to_number ratelimit={requests_per_unit=5, unit=DAY}","@timestamp":"2020-09-10T17:22:44.926037174Z","level":"debug"}
{"@message":"loading descriptor: key=messaging.to_number ratelimit={requests_per_unit=100, unit=DAY}","@timestamp":"2020-09-10T17:22:44.926048993Z","level":"debug"}
{"@message":"Listening for debug on ':6070'","@timestamp":"2020-09-10T17:22:44.926113905Z","level":"warning"}
{"@message":"Listening for gRPC on ':8081'","@timestamp":"2020-09-10T17:22:44.926182006Z","level":"warning"}
{"@message":"Listening for HTTP on ':8080'","@timestamp":"2020-09-10T17:22:44.926227031Z","level":"warning"}
{"@message":"waiting for runtime update","@timestamp":"2020-09-10T17:22:44.926267808Z","level":"debug"}
```

## GRPC Keepalive

Client-side GRPC DNS re-resolution in scenarios with auto scaling enabled might not work as expected and the current workaround is to [configure connection keepalive](https://github.com/grpc/grpc/issues/12295#issuecomment-382794204) on server-side.
The behavior can be fixed by configuring the following env variables for the ratelimit server:

- `GRPC_MAX_CONNECTION_AGE`: a duration for the maximum amount of time a connection may exist before it will be closed by sending a GoAway. A random jitter of +/-10% will be added to MaxConnectionAge to spread out connection storms.
- `GRPC_MAX_CONNECTION_AGE_GRACE`: an additive period after MaxConnectionAge after which the connection will be forcibly closed.

# Request Fields

For information on the fields of a Ratelimit gRPC request please read the information
on the RateLimitRequest message type in the Ratelimit [proto file.](https://github.com/envoyproxy/envoy/blob/master/api/envoy/service/ratelimit/v3/rls.proto)

# GRPC Client

The [gRPC client](https://github.com/envoyproxy/ratelimit/blob/master/src/client_cmd/main.go) will interact with ratelimit server and tell you if the requests are over limit.

## Commandline flags

- `-dial_string`: used to specify the address of ratelimit server. It defaults to `localhost:8081`.
- `-domain`: used to specify the domain.
- `-descriptors`: used to specify one descriptor. You can pass multiple descriptors like following:

```
go run main.go -domain test \
-descriptors name=foo,age=14 -descriptors name=bar,age=18
```

# Global ShadowMode

There is a global shadow-mode which can make it easier to introduce rate limiting into an existing service landscape. It will override whatever result is returned by the regular rate limiting process.

## Configuration

The global shadow mode is configured with an environment variable

Setting environment variable `SHADOW_MODE` to `true` will enable the feature.

## Statistics

There is an additional service-level statistics generated that will increment whenever the global shadow mode has overridden a rate limiting result.

# Statistics

The rate limit service generates various statistics for each configured rate limit rule that will be useful for end
users both for visibility and for setting alarms. Ratelimit uses [gostats](https://github.com/lyft/gostats) as its statistics library. Please refer
to [gostats' documentation](https://godoc.org/github.com/lyft/gostats) for more information on the library.

Rate Limit Statistic Path:

```
ratelimit.service.rate_limit.DOMAIN.KEY_VALUE.STAT
```

DOMAIN:

- As specified in the domain value in the YAML runtime file

KEY_VALUE:

- A combination of the key value
- Nested descriptors would be suffixed in the stats path

STAT:

- near_limit: Number of rule hits over the NearLimit ratio threshold (currently 80%) but under the threshold rate.
- over_limit: Number of rule hits exceeding the threshold rate
- total_hits: Number of rule hits in total
- shadow_mode: Number of rule hits where shadow_mode would trigger and override the over_limit result

To use a custom near_limit ratio threshold, you can specify with `NEAR_LIMIT_RATIO` environment variable. It defaults to `0.8` (0-1 scale). These are examples of generated stats for some configured rate limit rules from the above examples:

```
ratelimit.service.rate_limit.mongo_cps.database_default.over_limit: 0
ratelimit.service.rate_limit.mongo_cps.database_default.total_hits: 2846
ratelimit.service.rate_limit.mongo_cps.database_users.over_limit: 0
ratelimit.service.rate_limit.mongo_cps.database_users.total_hits: 2939
ratelimit.service.rate_limit.messaging.message_type_marketing.to_number.over_limit: 0
ratelimit.service.rate_limit.messaging.message_type_marketing.to_number.total_hits: 0
ratelimit.service.rate_limit.messaging.auth-service.over_limit.total_hits: 1
ratelimit.service.rate_limit.messaging.auth-service.over_limit.over_limit: 1
ratelimit.service.rate_limit.messaging.auth-service.over_limit.shadow_mode: 1
```

## Statistics options

1. `EXTRA_TAGS`: set to `"<k1:v1>,<k2:v2>"` to tag all emitted stats with the provided tags. You might want to tag build commit or release version, for example.

# HTTP Port

The ratelimit service listens to HTTP 1.1 (by default on port 8080) with two endpoints:

1. /healthcheck → return a 200 if this service is healthy
1. /json → HTTP 1.1 endpoint for interacting with ratelimit service

## /json endpoint

Takes an HTTP POST with a JSON body of the form e.g.

```json
{
  "domain": "dummy",
  "descriptors": [
    { "entries": [{ "key": "one_per_day", "value": "something" }] }
  ]
}
```

The service will return an http 200 if this request is allowed (if no ratelimits exceeded) or 429 if one or more
ratelimits were exceeded.

The response is a RateLimitResponse encoded with
[proto3-to-json mapping](https://developers.google.com/protocol-buffers/docs/proto3#json):

```json
{
  "overallCode": "OVER_LIMIT",
  "statuses": [
    {
      "code": "OVER_LIMIT",
      "currentLimit": {
        "requestsPerUnit": 1,
        "unit": "MINUTE"
      }
    },
    {
      "code": "OK",
      "currentLimit": {
        "requestsPerUnit": 2,
        "unit": "MINUTE"
      },
      "limitRemaining": 1
    }
  ]
}
```

# Debug Port

The debug port can be used to interact with the running process.

```
$ curl 0:6070/
/debug/pprof/: root of various pprof endpoints. hit for help.
/rlconfig: print out the currently loaded configuration for debugging
/stats: print out stats
```

You can specify the debug server address with the `DEBUG_HOST` and `DEBUG_PORT` environment variables. They currently default to `0.0.0.0` and `6070` respectively.

# Local Cache

Ratelimit optionally uses [freecache](https://github.com/coocood/freecache) as its local caching layer, which stores the over-the-limit cache keys, and thus avoids reading the
redis cache again for the already over-the-limit keys. The local cache size can be configured via `LocalCacheSizeInBytes` in the [settings](https://github.com/envoyproxy/ratelimit/blob/master/src/settings/settings.go).
If `LocalCacheSizeInBytes` is 0, local cache is disabled.

# Redis

Ratelimit uses Redis as its caching layer. Ratelimit supports two operation modes:

1. One Redis server for all limits.
1. Two Redis instances: one for per second limits and another one for all other limits.

As well Ratelimit supports TLS connections and authentication. These can be configured using the following environment variables:

1. `REDIS_TLS` & `REDIS_PERSECOND_TLS`: set to `"true"` to enable a TLS connection for the specific connection type.
1. `REDIS_TLS_CLIENT_CERT`, `REDIS_TLS_CLIENT_KEY`, and `REDIS_TLS_CACERT` to provides files to specify a TLS connection configuration to Redis server that requires client certificate verification. (This is effective when `REDIS_TLS` or `REDIS_PERSECOND_TLS` is set to to `"true"`).
1. `REDIS_AUTH` & `REDIS_PERSECOND_AUTH`: set to `"password"` to enable password-only authentication to the redis host.
1. `REDIS_AUTH` & `REDIS_PERSECOND_AUTH`: set to `"username:password"` to enable username-password authentication to the redis host.
1. `CACHE_KEY_PREFIX`: a string to prepend to all cache keys

## Redis type

Ratelimit supports different types of redis deployments:

1. Single instance (default): Talk to a single instance of redis, or a redis proxy (e.g. https://www.envoyproxy.io/docs/envoy/latest/intro/arch_overview/other_protocols/redis)
1. Sentinel: Talk to a redis deployment with sentinel instances (see https://redis.io/topics/sentinel)
1. Cluster: Talk to a redis in cluster mode (see https://redis.io/topics/cluster-spec)

The deployment type can be specified with the `REDIS_TYPE` / `REDIS_PERSECOND_TYPE` environment variables. Depending on the type defined, the `REDIS_URL` and `REDIS_PERSECOND_URL` are expected to have the following formats:

1. "single": Depending on the socket type defined, either a single hostname:port pair or a unix domain socket reference.
1. "sentinel": A comma separated list with the first string as the master name of the sentinel cluster followed by hostname:port pairs. The list size should be >= 2. The first item is the name of the master and the rest are the sentinels.
1. "cluster": A comma separated list of hostname:port pairs with all the nodes in the cluster.

## Pipelining

By default, for each request, ratelimit will pick up a connection from pool, write multiple redis commands in a single write then reads their responses in a single read. This reduces network delay.

For high throughput scenarios, ratelimit also support [implicit pipelining](https://github.com/mediocregopher/radix/blob/v3.5.1/pool.go#L238) . It can be configured using the following environment variables:

1. `REDIS_PIPELINE_WINDOW` & `REDIS_PERSECOND_PIPELINE_WINDOW`: sets the duration after which internal pipelines will be flushed.
   If window is zero then implicit pipelining will be disabled.
1. `REDIS_PIPELINE_LIMIT` & `REDIS_PERSECOND_PIPELINE_LIMIT`: sets maximum number of commands that can be pipelined before flushing.
   If limit is zero then no limit will be used and pipelines will only be limited by the specified time window.

`implicit pipelining` is disabled by default. To enable it, you can use default values [used by radix](https://github.com/mediocregopher/radix/blob/v3.5.1/pool.go#L278) and tune for the optimal value.

## One Redis Instance

To configure one Redis instance use the following environment variables:

1. `REDIS_SOCKET_TYPE`
1. `REDIS_URL`
1. `REDIS_POOL_SIZE`
1. `REDIS_TYPE` (optional)

This setup will use the same Redis server for all limits.

## Two Redis Instances

To configure two Redis instances use the following environment variables:

1. `REDIS_SOCKET_TYPE`
1. `REDIS_URL`
1. `REDIS_POOL_SIZE`
1. `REDIS_PERSECOND`: set this to `"true"`.
1. `REDIS_PERSECOND_SOCKET_TYPE`
1. `REDIS_PERSECOND_URL`
1. `REDIS_PERSECOND_POOL_SIZE`
1. `REDIS_PERSECOND_TYPE` (optional)

This setup will use the Redis server configured with the `_PERSECOND_` vars for
per second limits, and the other Redis server for all other limits.

## Health Checking for Redis Active Connection

To configure whether to return health check failure if there is no active redis connection

1. `REDIS_HEALTH_CHECK_ACTIVE_CONNECTION` : (default is "false")

# Memcache

Experimental Memcache support has been added as an alternative to Redis in v1.5.

To configure a Memcache instance use the following environment variables instead of the Redis variables:

1. `MEMCACHE_HOST_PORT=`: a comma separated list of hostname:port pairs for memcache nodes (mutually exclusive with `MEMCACHE_SRV`)
1. `MEMCACHE_SRV=`: an SRV record to lookup hosts from (mutually exclusive with `MEMCACHE_HOST_PORT`)
1. `MEMCACHE_SRV_REFRESH=0`: refresh the list of hosts every n seconds, if 0 no refreshing will happen, supports duration suffixes: "ns", "us" (or "µs"), "ms", "s", "m", "h".
1. `BACKEND_TYPE=memcache`
1. `CACHE_KEY_PREFIX`: a string to prepend to all cache keys
1. `MEMCACHE_MAX_IDLE_CONNS=2`: the maximum number of idle TCP connections per memcache node, `2` is the default of the underlying library

With memcache mode increments will happen asynchronously, so it's technically possible for
a client to exceed quota briefly if multiple requests happen at exactly the same time.

Note that Memcache has a max key length of 250 characters, so operations referencing very long
descriptors will fail. Descriptors sent to Memcache should not contain whitespaces or control characters.

When using multiple memcache nodes in `MEMCACHE_HOST_PORT=`, one should provide the identical list of memcache nodes
to all ratelimiter instances to ensure that a particular cache key is always hashed to the same memcache node.

# Custom headers

Ratelimit service can be configured to return custom headers with the ratelimit information. It will populate the response_headers_to_add as part of the [RateLimitResponse](https://www.envoyproxy.io/docs/envoy/latest/api-v3/service/ratelimit/v3/rls.proto#service-ratelimit-v3-ratelimitresponse).

The following environment variables control the custom response feature:

1. `LIMIT_RESPONSE_HEADERS_ENABLED` - Enables the custom response headers
1. `LIMIT_LIMIT_HEADER` - The default value is "RateLimit-Limit", setting the environment variable will specify an alternative header name
1. `LIMIT_REMAINING_HEADER` - The default value is "RateLimit-Remaining", setting the environment variable will specify an alternative header name
1. `LIMIT_RESET_HEADER` - The default value is "RateLimit-Reset", setting the environment variable will specify an alternative header name

You may use the following commands to quickly setup a openTelemetry collector together with a Jaeger all-in-one binary for quickstart:

```bash
docker run --name otlp -d -p 4318 -p 4317 -v examples/otlp-collector:/tmp/otlp-collector otel/opentelemetry-collector:0.48.0 -- --config /tmp/otlp-collector/config.yaml
otelcol-contrib --config examples/otlp-collector/config.yaml

docker run -d --name jaeger -p 16686:16686 -p 14250:14250 jaegertracing/all-in-one:1.33
```

# Tracing

Ratelimit service supports exporting spans in OLTP format. See [OpenTelemetry](https://opentelemetry.io/) for more information.

The following environment variables control the tracing feature:

1. `TRACING_ENABLED` - Enables the tracing feature. Only "true" and "false"(default) are allowed in this field.
1. `TRACING_EXPORTER_PROTOCOL` - Controls the protocol of exporter in tracing feature. Only "http"(default) and "grpc" are allowed in this field.
1. `TRACING_SERVICE_NAME` - Controls the service name appears in tracing span. The default value is "RateLimit".
1. `TRACING_SERVICE_NAMESPACE` - Controls the service namespace appears in tracing span. The default value is empty.
1. `TRACING_SERVICE_INSTANCE_ID` - Controls the service instance id appears in tracing span. It is recommended to put the pod name or container name in this field. The default value is a randomly generated version 4 uuid if unspecified.
1. Other fields in [OTLP Exporter Documentation](https://github.com/open-telemetry/opentelemetry-specification/blob/v1.8.0/specification/protocol/exporter.md). These section needs to be correctly configured in order to enable the exporter to export span to the correct destination.
1. `TRACING_SAMPLING_RATE` - Controls the sampling rate, defaults to 1 which means always sample. Valid range: 0.0-1.0. For high volume services, adjusting the sampling rate is recommended.

# mTLS

Ratelimit supports mTLS when Envoy sends requests to the service.

The following environment variables control the mTLS feature:

The following variables can be set to enable mTLS on the Ratelimit service.

1. `GRPC_SERVER_USE_TLS` - Enables gprc connections to server over TLS
1. `GRPC_SERVER_TLS_CERT` - Path to the file containing the server cert chain
1. `GRPC_SERVER_TLS_KEY` - Path to the file containing the server private key
1. `GRPC_CLIENT_TLS_CACERT` - Path to the file containing the client CA certificate.
1. `GRPC_CLIENT_TLS_SAN` - (Optional) DNS Name to validate from the client cert during mTLS auth

In the envoy config use, add the `transport_socket` section to the ratelimit service cluster config

```yaml
"name": "ratelimit"
"transport_socket":
  "name": "envoy.transport_sockets.tls"
  "typed_config":
    "@type": "type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.UpstreamTlsContext"
    "common_tls_context":
      "tls_certificates":
        - "certificate_chain":
            "filename": "/opt/envoy/tls/ratelimit-client-cert.pem"
          "private_key":
            "filename": "/opt/envoy/tls/ratelimit-client-key.pem"
      "validation_context":
        "match_subject_alt_names":
          - "exact": "ratelimit.server.dnsname"
        "trusted_ca":
          "filename": "/opt/envoy/tls/ratelimit-server-ca.pem"
```

# Contact

- [envoy-announce](https://groups.google.com/forum/#!forum/envoy-announce): Low frequency mailing
  list where we will email announcements only.
- [envoy-users](https://groups.google.com/forum/#!forum/envoy-users): General user discussion.
  Please add `[ratelimit]` to the email subject.
- [envoy-dev](https://groups.google.com/forum/#!forum/envoy-dev): Envoy developer discussion (APIs,
  feature design, etc.). Please add `[ratelimit]` to the email subject.
- [Slack](https://envoyproxy.slack.com/): Slack, to get invited go [here](http://envoyslack.cncf.io).
  We have the IRC/XMPP gateways enabled if you prefer either of those. Once an account is created,
  connection instructions for IRC/XMPP can be found [here](https://envoyproxy.slack.com/account/gateways).
  The `#ratelimit-users` channel is used for discussions about the ratelimit service.
