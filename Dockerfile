FROM golang:1.27.3-alpine3.24@sha256:fa4881266890ef051f0ce63afdfaccb69bbe8186084387f4fafd60d7ecbabc71 AS go-test-base
RUN apk add --no-cache build-base
WORKDIR /src
COPY go.mod go.sum ./
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY tests/fixtures/managed-cameras.json ./tests/fixtures/managed-cameras.json

FROM go-test-base AS go-race
RUN go test -race ./...

FROM go-test-base AS go-vet
RUN go vet ./...

FROM golang:1.27.3-alpine3.24@sha256:fa4881266890ef051f0ce63afdfaccb69bbe8186084387f4fafd60d7ecbabc71 AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /gateway ./cmd/gateway

FROM alpine:3.24@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a
RUN apk add --no-cache ca-certificates ffmpeg && addgroup -S xlg && adduser -S -G xlg xlg && mkdir -p /data && chown xlg:xlg /data
COPY --from=build /gateway /usr/local/bin/xlg
VOLUME ["/data"]
EXPOSE 8099/tcp 8554/tcp
USER xlg
ENTRYPOINT ["/usr/local/bin/xlg"]
CMD ["-config", "/config/config.json"]
