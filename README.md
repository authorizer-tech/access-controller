# access-controller

[![Latest Release](https://img.shields.io/github/v/release/authorizer-tech/access-controller)](https://github.com/authorizer-tech/access-controller/releases/latest)
[![Go Report Card](https://goreportcard.com/badge/github.com/authorizer-tech/access-controller)](https://goreportcard.com/report/github.com/authorizer-tech/access-controller)
[![Slack](https://img.shields.io/badge/slack-%23authorizer--tech-green)](https://authorizer-tech.slack.com)

An implementation of a distributed access-control server that is based on [Google Zanzibar](https://research.google/pubs/pub48190/) - "Google's Consistent, Global Authorization System".

An instance of an `access-controller` is similar to the `aclserver` implementation called out in the paper. A cluster of access-controllers implement the functional equivalent of the Zanzibar `aclserver` cluster.

# Getting Started
If you want to setup an instance of the Authorizer platform as a whole, browse the API References, or just brush up on the concepts and design of the platform, take a look at the [official platform documentation](https://authorizer-tech.github.io/docs/overview/introduction). If you're only interested in running the access-controller then continue on.

## Setup a Cluster
An access-controller server supports single node or multi-node (clustered) topologies. Instructions for running the server with these topologies are outlined below.

To gain the benefits of the distributed query model that the access-controller implements, it is recommend to run a large cluster. Doing so will help distribute query load across more nodes within the cluster. The underlying cluster membership list is based on Hashicorp's [`memberlist`](https://github.com/hashicorp/memberlist)

> a library that manages cluster membership and member failure detection using a gossip based protocol.

A cluster should be able to suport hundreds of nodes. If you find otherwise, please [submit an issue](https://github.com/authorizer-tech/access-controller/issues/new).

### Docker Compose
[`docker-compose.yml`](./docker/docker-compose.yml) provides an example of how to setup a multi-node cluster using Docker and is a great way to get started quickly.

```console
$ docker compose -f docker/docker-compose.yml up
```

### Kubernetes (Recommended)
Take a look at our [official Helm chart](https://authorizer-tech.github.io/helm-charts/access-controller).

### Pre-compiled Binaries
Download the [latest release](https://github.com/authorizer-tech/access-controller/releases/latest) and extract it.

#### Pre-requisites
To run an access-controller you must have a running CockroachDB database. Take a look at setting up [CockroachDB with Docker](https://www.cockroachlabs.com/docs/stable/start-a-local-cluster-in-docker-mac.html).

#### Single Node
```console
$ ./bin/access-controller
```

#### Multi-node
Start a multi-node cluster by starting multiple independent servers and use the `-join` flag
to join the node to an existing cluster.

```console
$ ./bin/access-controller -node-port 7946 -grpc-port 50052
$ ./bin/access-controller -node-port 7947 -grpc-port 50053 -join 127.0.0.1:7946
$ ./bin/access-controller -node-port 7948 -grpc-port 50054 -join 127.0.0.1:7947
```

## Next Steps...
Take a look at the examples of how to:
* [Add a Namespace Configuration](https://authorizer-tech.github.io/docs/getting-started/add-namespace-config)
* [Write a Relation Tuple](https://authorizer-tech.github.io/docs/getting-started/write-relation-tuple)
* [Check a Subject's Access](https://authorizer-tech.github.io/docs/getting-started/check-access)

Don't hesitate to browse the official [Documentation](https://authorizer-tech.github.io/docs/overview/introduction), [API Reference](https://authorizer-tech.github.io/docs/api-reference/overview) and [Examples](https://authorizer-tech.github.io/docs/overview/examples/examples-intro).

# Community
The access-controller is an open-source project and we value and welcome new contributors and members
of the community. Here are ways to get in touch with the community:

* Slack: [#authorizer-tech](https://authorizer-tech.slack.com)
* Issue Tracker: [GitHub Issues](https://github.com/authorizer-tech/access-controller/issues)