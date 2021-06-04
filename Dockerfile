FROM alpine
COPY access-controller /bin
COPY testdata /bin/testdata
COPY db/migrations /bin/db/migrations
WORKDIR /bin