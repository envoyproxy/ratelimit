FROM golang:1.23.6@sha256:927112936d6b496ed95f55f362cc09da6e3e624ef868814c56d55bd7323e0959 AS build
WORKDIR /ratelimit

ENV GOPROXY=https://proxy.golang.org
COPY go.mod go.sum /ratelimit/
RUN go mod download

COPY src src
COPY script script

RUN CGO_ENABLED=0 GOOS=linux go build -o /go/bin/ratelimit -ldflags="-w -s" -v github.com/envoyproxy/ratelimit/src/service_cmd

FROM alpine:3.20.3@sha256:1e42bbe2508154c9126d48c2b8a75420c3544343bf86fd041fb7527e017a4b4a AS final
RUN apk --no-cache add ca-certificates && apk --no-cache update
COPY --from=build /go/bin/ratelimit /bin/ratelimit
