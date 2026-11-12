FROM golang:1.27.3-alpine3.24@sha256:fa4881266890ef051f0ce63afdfaccb69bbe8186084387f4fafd60d7ecbabc71 AS rtsp-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY tests/mocks/rtsp_mock.go /src/rtsp_mock.go
RUN CGO_ENABLED=0 go build -trimpath -o /rtsp_mock /src/rtsp_mock.go

FROM python:3.13-alpine@sha256:7984cf56ceaddebe4fefeb75d64000b9f45d8b77b11a09b24524da6cdc3af0c8
RUN apk add --no-cache ffmpeg
WORKDIR /app
COPY tests/mocks/onvif_mock.py /app/onvif_mock.py
COPY tests/mocks/start_mock.sh /app/start_mock.sh
COPY --from=rtsp-build /rtsp_mock /app/rtsp_mock
RUN chmod 755 /app/start_mock.sh /app/rtsp_mock
USER 65534:65534
ENTRYPOINT ["/bin/sh", "/app/start_mock.sh"]
