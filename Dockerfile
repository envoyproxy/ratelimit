FROM golang:1.7

# create directory for ratelimit code
RUN mkdir -p /go/src/github.com/lyft/ratelimit/
WORKDIR /go/src/github.com/lyft/ratelimit/
COPY . .

# ratelimit startup
RUN export REDIS_SOCKET_TYPE=tcp
RUN export REDIS_URL=localhost:6379
RUN make bootstrap
RUN make compile
RUN USE_STATSD=false LOG_LEVEL=debug REDIS_SOCKET_TYPE=tcp REDIS_URL=localhost:6379 RUNTIME_ROOT=/home/user/src/runtime/data RUNTIME_SUBDIRECTORY=ratelimit
