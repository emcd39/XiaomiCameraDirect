FROM python:3.13-alpine@sha256:7984cf56ceaddebe4fefeb75d64000b9f45d8b77b11a09b24524da6cdc3af0c8 AS frigate-source

ARG FRIGATE_HA_TAG=v5.15.6
ARG FRIGATE_HA_COMMIT=3f1469c

RUN apk add --no-cache git \
    && git clone --depth 1 --branch "${FRIGATE_HA_TAG}" https://github.com/blakeblackshear/frigate-hass-integration.git /src \
    && test "$(git -C /src rev-parse --short=7 HEAD)" = "${FRIGATE_HA_COMMIT}" \
    && printf '%s %s\n' "${FRIGATE_HA_TAG}" "${FRIGATE_HA_COMMIT}" > /src/custom_components/frigate/.xlg-test-source

FROM python:3.13-alpine@sha256:7984cf56ceaddebe4fefeb75d64000b9f45d8b77b11a09b24524da6cdc3af0c8

COPY --from=frigate-source /src/custom_components/frigate /frigate

CMD ["sh", "-c", "cat /frigate/.xlg-test-source"]
