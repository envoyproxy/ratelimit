job_name  = "controlplane-ratelimit"
app_count = 2

network = {
  mode = "host"
  ports = {
    "grpc" = {
      check_type = "grpc"
    }
    "admin" = {
      check_type = "http"
      check_path = "/healthcheck"
    }
    "debug" = {
      check_type = "tcp"
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
    key   = "MAX_SLEEPING_ROUTINES"
    value = "64"
  },
  {
    key   = "GRPC_PORT"
    value = "$${NOMAD_PORT_grpc}"
  },
  {
    key   = "PORT",
    value = "$${NOMAD_PORT_admin}"
  },
  {
    key = "DEBUG_PORT"
    value = "$${NOMAD_PORT_debug}"
  }
  {
    key   = "LOG_LEVEL",
    value = "debug"
  },
  {
    key = "FORCE_START_WITHOUT_INITIAL_CONFIG"
    value = "true"
  },
  {
    key = "CONFIG_TYPE"
    value = "GRPC_XDS_SOTW"
  },
  {
    key = "CONFIG_GRPC_XDS_NODE_ID"
    value = "controlplane-ratelimit"
  },
  {
    key = "CONFIG_GRPC_XDS_SERVER_URL"
    value = "localhost:9599"
  }
]

env_secrets = [
  {
    source = "kt_secrets::redis_api_general_master_password"
    dest   = "REDIS_AUTH"
  }
]

args = [
  "/usr/bin/ratelimit-server"
]
