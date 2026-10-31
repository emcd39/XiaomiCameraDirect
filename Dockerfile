FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /gateway ./cmd/gateway

FROM alpine:3.21
RUN apk add --no-cache ca-certificates && addgroup -S xlg && adduser -S -G xlg xlg
COPY --from=build /gateway /usr/local/bin/xlg
USER xlg
ENTRYPOINT ["/usr/local/bin/xlg"]
CMD ["-config", "/config/config.json"]
