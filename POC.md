# Plan based rate limits PoC
## Rate limits configuration

* account_id: 1111 - plan: team - limits: 2 RPM
* account_id: 2222 - plan: professional - limits: 5 RPM
* account_id: 3333 - plan: enterprise - limits: 10 RPM
* account_id: 4444 - plan: api_boost - limits: 20 RPM
* account wide API requests - plan independent - 100 RPM (EnvoyProxy handles multiple rate limits counters in parallel)
* mobile requests are extemped

rate limits [configuration](https://github.com/priviterag/ratelimit/blob/main/examples/ratelimit/config/example.yaml)

EnvoyProxy [configuration](https://github.com/priviterag/ratelimit/blob/main/examples/envoy/proxy.yaml)

## How accounts are linked to plans
There is an http filter (Lua) that given an account id adds an additional header (plan). In a real life scenario, this logic should be replaced by a filter that calls out a web service.

```lua
function envoy_on_request(request_handle)
  local mobile_token = request_handle:headers():get("mobile_token")

  if mobile_token
  then
    request_handle:headers():add("plan", "mobile_sdk")
    return
  end

  local account_id = request_handle:headers():get("account_id")

  if account_id == "1111"
  then
    request_handle:headers():add("plan", "team")
  elseif account_id == "2222"
  then
    request_handle:headers():add("plan", "professional")
  elseif account_id == "3333"
  then
    request_handle:headers():add("plan", "enterprise")
  elseif account_id == "4444"
  then
    request_handle:headers():add("plan", "api_boost")
  end
end
```

## Full test environment - Configure rate limits through files

To run a fully configured environment to demo Envoy based rate limiting, run:

```bash
CONFIG_TYPE=FILE docker-compose -f docker-compose-example.yml up --build --remove-orphans
```

This will run ratelimit, redis, prom-statsd-exporter and two Envoy containers such that you can demo rate limiting by hitting the below endpoints.

```bash
curl -i localhost:8888/api -H "account_id: 1111"
curl -i localhost:8888/api -H "account_id: 1111" -H "mobile_token: foo" # this traffic is extempted
```

Edit `examples/ratelimit/config/example.yaml` to test different rate limit configs. Hot reloading is enabled.

The descriptors in `example.yaml` and the actions in `examples/envoy/proxy.yaml` should give you a good idea on how to configure rate limits.

To see the metrics (prometheus) in the example

```bash
watch -n 1 'curl http://localhost:9102/metrics|grep account_id'
```
