FROM golang:1.26.5-alpine3.23@sha256:622e56dbc11a8cfe87cafa2331e9a201877271cbff918af53d3be315f3da88cc AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/dialer .

FROM alpine:3.23@sha256:fd791d74b68913cbb027c6546007b3f0d3bc45125f797758156952bc2d6daf40
RUN addgroup -S -g 10001 dialer \
    && adduser -S -D -H -u 10001 -G dialer dialer \
    && mkdir -p /var/lib/dialer/media \
    && chown -R dialer:dialer /var/lib/dialer
COPY --from=build --chown=dialer:dialer /out/dialer /usr/local/bin/dialer
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/dialer"]
