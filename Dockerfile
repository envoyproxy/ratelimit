FROM golang:1.14 AS build
WORKDIR /ratelimit

ENV GOPROXY=https://proxy.golang.org
COPY go.mod go.sum /ratelimit/
RUN go mod download

COPY src src
COPY script script
COPY test test

ARG BUILDPLATFORM
ARG TARGETPLATFORM
RUN if [ "$BUILDPLATFORM" = "$TARGETPLATFORM" ]; then go test -v -race github.com/replicon/ratelimit/... ; fi

RUN CGO_ENABLED=0 GOOS=linux go build -o /go/bin/ratelimit -ldflags="-w -s" -v github.com/replicon/ratelimit/src/service_cmd && \
 CGO_ENABLED=0 GOOS=linux go build -o /go/bin/ratelimit_config_check -ldflags="-w -s" -v github.com/replicon/ratelimit/src/config_check_cmd


FROM python:3.10-alpine3.16 AS final

RUN apk update && apk add --update ca-certificates curl && \
  pip3 install ipaddress awscli && \
  mkdir -p /srv/runtime_data/current/config && \
  mkdir -p /srv/runtime_data/current/validate_config

COPY --from=build /go/bin/ratelimit /bin/ratelimit
COPY --from=build /go/bin/ratelimit_config_check /bin/ratelimit_config_check
COPY entrypoint.sh /entrypoint.sh
COPY sync_config.sh /sync_config.sh
COPY metrics /metrics
ENTRYPOINT [ "/entrypoint.sh" ]
