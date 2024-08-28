app_count = 1


env_vars = [
  {
    key = "TRACING_ENABLED"
    value = "true"
  },
  {
    key = "TRACING_SERVICE_NAME"
    value = "controlplane-ratelimit"
  },
  {
    key = "TRACING_SERVICE_INSTANCE_ID"
    value = "$${NOMAD_ALLOC_ID}"
  },
  {
    key = "TRACING_EXPORTER_PROTOCOL"
    value = "grpc"
  },
  {
    key = "OTEL_EXPORTER_OTLP_ENDPOINT"
    value = "127.0.0.1:26000"
  }
]
