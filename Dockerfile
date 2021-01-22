FROM golang:1.14 AS build
WORKDIR /ratelimit

ENV GOPROXY=https://proxy.golang.org
COPY go.mod go.sum /ratelimit/
RUN go mod download

COPY src src
COPY script script

RUN CGO_ENABLED=0 GOOS=linux go build -o /go/bin/ratelimit -ldflags="-w -s" -v github.com/envoyproxy/ratelimit/src/service_cmd

FROM alpine:3.11 AS final
RUN apk --no-cache add ca-certificates

FROM ubuntu:latest as install
RUN apt-get update && apt-get install -y supervisor
RUN mkdir -p /var/log/supervisor
COPY --from=build /go/bin/ratelimit /bin/ratelimit
COPY supervisord.conf /etc/supervisor/conf.d/supervisord.conf

ENTRYPOINT ["/usr/bin/supervisord"]