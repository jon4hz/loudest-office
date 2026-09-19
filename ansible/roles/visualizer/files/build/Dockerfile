# Built on the raspi by podman-compose; spectrum is cross-compiled by `task visualizer:build`.
FROM docker.io/alpine:3.22
# spectrum captures through arecord
RUN apk add --no-cache alsa-utils
COPY spectrum /spectrum
RUN chmod 0755 /spectrum
ENTRYPOINT ["/spectrum"]
