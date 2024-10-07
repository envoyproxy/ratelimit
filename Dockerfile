FROM golang:1.23.2@sha256:adee809c2d0009a4199a11a1b2618990b244c6515149fe609e2788ddf164bd10 AS build
WORKDIR /ratelimit

ENV GOPROXY=https://proxy.golang.org
COPY go.mod go.sum /ratelimit/
RUN go mod download

COPY src src
COPY script script

RUN CGO_ENABLED=0 GOOS=linux go build -o /go/bin/ratelimit -ldflags="-w -s" -v github.com/envoyproxy/ratelimit/src/service_cmd

FROM alpine:3.20.2@sha256:0a4eaa0eecf5f8c050e5bba433f58c052be7587ee8af3e8b3910ef9ab5fbe9f5 AS final
RUN apk --no-cache add ca-certificates && apk --no-cache update
COPY --from=build /go/bin/ratelimit /bin/ratelimit
