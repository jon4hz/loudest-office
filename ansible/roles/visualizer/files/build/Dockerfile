# Built on the raspi by podman-compose; spectrum is cross-compiled by `task visualizer:build`,
# which also copies the sendspin-pipe sources next to it.
# sendspin-pipe needs cgo (miniaudio, libopus), so it is compiled here, on the target.
# The builder's Alpine release must match the runtime's below: sendspin-pipe links dynamically
# against musl/libopus/libopusfile, which are not ABI-stable across Alpine releases.
# golang:1.27-alpine3.22 doesn't exist (checked via skopeo list-tags); 3.24 is the newest that does.
FROM docker.io/golang:1.27-alpine3.24 AS pipe
RUN apk add --no-cache build-base opus-dev opusfile-dev
WORKDIR /src
COPY sendspin-pipe/ .
RUN go build -trimpath -ldflags "-s -w" -o /sendspin-pipe .

FROM docker.io/alpine:3.24
# spectrum captures through arecord, or through sendspin-pipe (opus at run time); tzdata for the clock bubble's TZ
RUN apk add --no-cache alsa-utils tzdata opus opusfile
COPY --from=pipe /sendspin-pipe /usr/local/bin/sendspin-pipe
COPY spectrum /spectrum
RUN chmod 0755 /spectrum
ENTRYPOINT ["/spectrum"]
