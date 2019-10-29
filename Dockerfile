FROM golang:1.13.3 AS base
WORKDIR /go/src/github.com/lyft/ratelimit
COPY go.mod go.sum ./
RUN go mod download

FROM base as build
COPY src src
COPY script script
COPY proto proto
RUN CGO_ENABLED=0 GOOS=linux go build -o /usr/local/bin/ratelimit -ldflags="-w -s" -v ./src/service_cmd

FROM alpine:3.8 AS final
RUN apk --no-cache add ca-certificates
COPY --from=build /usr/local/bin/ratelimit /bin/ratelimit
