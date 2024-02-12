FROM golang:1.22.0@sha256:ef61a20960397f4d44b0e729298bf02327ca94f1519239ddc6d91689615b1367 AS build
WORKDIR /ratelimit

ENV GOPROXY=https://proxy.golang.org
COPY go.mod go.sum /ratelimit/
RUN go mod download

COPY src src
COPY script script

RUN CGO_ENABLED=0 GOOS=linux go build -o /go/bin/ratelimit -ldflags="-w -s" -v github.com/envoyproxy/ratelimit/src/service_cmd

FROM alpine:3.18.5@sha256:34871e7290500828b39e22294660bee86d966bc0017544e848dd9a255cdf59e0 AS final
RUN apk --no-cache add ca-certificates && apk --no-cache update
COPY --from=build /go/bin/ratelimit /bin/ratelimit
