FROM golang:1.26.3@sha256:efaccb5b497e90df3ebe5216cc25cd9f98e73874e2d638b56e38d4a3f098c41c AS build
WORKDIR /xds-server

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o /go/bin/xds-server -v main/main.go

FROM alpine:3.20@sha256:77726ef6b57ddf65bb551896826ec38bc3e53f75cdde31354fbffb4f25238ebd AS final
RUN apk --no-cache add ca-certificates && apk --no-cache update
COPY --from=build /go/bin/xds-server /bin/xds-server
ENTRYPOINT [ "/bin/xds-server" ]
