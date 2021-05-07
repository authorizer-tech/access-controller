FROM alpine
COPY access-controller /bin
COPY testdata /bin/testdata
WORKDIR /bin