FROM golang:1.10.4 AS build
WORKDIR /go/src/github.com/lyft/ratelimit

COPY src src
COPY script script
COPY vendor vendor
COPY glide.yaml glide.yaml
COPY glide.lock glide.lock

RUN script/install-glide
RUN glide install

RUN CGO_ENABLED=0 GOOS=linux go build -o /usr/local/bin/ratelimit -ldflags="-w -s" -v github.com/lyft/ratelimit/src/service_cmd

FROM alpine:3.8 AS final
RUN apk --no-cache add ca-certificates
COPY --from=build /usr/local/bin/ratelimit /bin/ratelimit