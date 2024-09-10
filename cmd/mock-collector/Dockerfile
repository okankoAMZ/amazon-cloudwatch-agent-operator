# Get CA certificates from the Alpine package repo
FROM alpine:3.18 as certificates

RUN apk --no-cache add ca-certificates

# Start a new stage from scratch
FROM bash

ARG TARGETARCH

WORKDIR /root/

# Copy the certs
COPY --from=certificates /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

# Copy binary built on the host
COPY bin/mockcollector_${TARGETARCH} ./main

ENTRYPOINT ["./main"]
