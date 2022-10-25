# Example Rate-limit Configuration SotW xDS Server

This is an example of a trivial xDS V3 control plane server similar to the example server in [go-control-plane](https://github.com/envoyproxy/go-control-plane/tree/main/internal/example). It serves sample Rate limit configuration. You can run the example using the project top-level docker-compose-example.yml, e.g.:

```bash
export CONFIG_TYPE=GRPC_XDS_SOTW
docker-compose -f docker-compose-example.yml --profile xds-config up --build --remove-orphans
```

The docker-compose builds and runs the example server along with Rate limit server. The example server serves a configuration defined in [`resource.go`](resource.go). If everything works correctly, you can follow the [examples in project top-level README.md file](../../README.md#examples).

## Files

- [main/main.go](main/main.go) is the example program entrypoint. It instantiates the cache and xDS server and runs the xDS server process.
- [resource.go](resource.go) generates a `Snapshot` structure which describes the configuration that the xDS server serves to Envoy.
- [server.go](server.go) runs the xDS control plane server.
- [logger.go](logger.go) is the logger.
