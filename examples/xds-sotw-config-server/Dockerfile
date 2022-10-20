FROM golang:1.18 AS build
WORKDIR /xds-server

# ENV GOPROXY=https://proxy.golang.org
# COPY go.mod go.sum /xds-server/
# RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o /go/bin/xds-server -v main/main.go

FROM alpine:3.16 AS final
RUN apk --no-cache add ca-certificates && apk --no-cache update
COPY --from=build /go/bin/xds-server /bin/xds-server
ENTRYPOINT [ "/bin/xds-server" ]
