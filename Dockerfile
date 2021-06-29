FROM alpine
COPY access-controller /bin
COPY testdata /bin/testdata
COPY db/migrations /bin/db/migrations
RUN wget -q -O /bin/grpc_health_probe https://github.com/grpc-ecosystem/grpc-health-probe/releases/download/v0.4.2/grpc_health_probe-linux-amd64 && \
    chmod +x /bin/grpc_health_probe
WORKDIR /bin