module github.com/envoyproxy/ratelimit/examples/xds-sotw-config-server

go 1.18

// TODO: (renuka) Remove this replace once, https://github.com/envoyproxy/go-control-plane/pull/598 is merged
replace github.com/envoyproxy/go-control-plane => github.com/renuka-fernando/go-control-plane v0.0.1

require (
	github.com/envoyproxy/go-control-plane v0.10.1
	google.golang.org/grpc v1.45.0
	google.golang.org/protobuf v1.28.0
)

require (
	github.com/census-instrumentation/opencensus-proto v0.3.0 // indirect
	github.com/cncf/xds/go v0.0.0-20220314180256-7f1daf1720fc // indirect
	github.com/envoyproxy/protoc-gen-validate v0.6.7 // indirect
	github.com/golang/protobuf v1.5.2 // indirect
	golang.org/x/net v0.0.0-20210813160813-60bc85c4be6d // indirect
	golang.org/x/sys v0.0.0-20210816183151-1e6c022a8912 // indirect
	golang.org/x/text v0.3.7 // indirect
	google.golang.org/genproto v0.0.0-20220329172620-7be39ac1afc7 // indirect
)
