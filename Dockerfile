FROM docker.io/library/golang:1.19 AS builder
COPY . /src
WORKDIR /src
RUN CGO_ENABLED=0 go build -o podperconn ./cmd/podperconn/.

FROM alpine:latest
RUN apk --no-cache add ca-certificates tini
WORKDIR /opt/
COPY --from=builder /src/podperconn .
# TODO: add cert, etc. via volume
VOLUME /config
ENTRYPOINT ["/sbin/tini", "--"]
CMD ["./podperconn", "--deployment=/config/container.yml"]
