FROM golang:1.25-alpine3.22 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/dialer .

FROM alpine:3.22
RUN addgroup -S -g 10001 dialer \
    && adduser -S -D -H -u 10001 -G dialer dialer \
    && mkdir -p /var/lib/dialer/media \
    && chown -R dialer:dialer /var/lib/dialer
COPY --from=build --chown=dialer:dialer /out/dialer /usr/local/bin/dialer
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/dialer"]
