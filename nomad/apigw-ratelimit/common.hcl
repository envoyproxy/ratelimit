job_name  = "apigw-ratelimit"
app_count = 2

constraints = [
  {
    attribute = "$${meta.run_apigw-ratelimit}"
    value     = "true"
  }
]

network = {
  mode = "host"
  ports = {
    "grpc" = {
      port       = 9484
      check_type = "grpc"
    }
    "admin" = {
      port       = 9485
      check_type = "http"
      check_path = "/healthcheck"
    }
  }
}

env_vars = [
  {
    key   = "USE_STATSD"
    value = "false"
  },
  {
    key   = "REDIS_SOCKET_TYPE"
    value = "tcp"
  },
  {
    key   = "REDIS_URL"
    value = "redis://$${attr.unique.network.ip-address}:9489/11"
  },
  {
    key   = "RUNTIME_ROOT"
    value = "/run"
  },
  {
    key   = "RUNTIME_SUBDIRECTORY"
    value = "ratelimit"
  },
  {
    key   = "RUNTIME_WATCH_ROOT"
    value = "false"
  },
  {
    key   = "RUNTIME_IGNOREDOTFILES"
    value = "true"
  },
  {
    key   = "MAX_SLEEPING_ROUTINES"
    value = "64"
  },
  {
    key   = "GRPC_PORT"
    value = "9484"
  },
  {
    key   = "PORT",
    value = "9485"
  },
  {
    key   = "LOG_LEVEL",
    value = "info"
  }
]

env_secrets = [
  {
    source = "kt_secrets::redis_api_general_master_password"
    dest   = "REDIS_AUTH"
  }
]

docker_volumes = ["/data/service/apigw/ratelimit:/run/ratelimit/config"]

args = [
  "/usr/bin/ratelimit-server"
]
