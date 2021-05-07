# access-controller

An implementation of a distributed access-control server that is based on [Google Zanzibar](https://research.google/pubs/pub48190/) - "Google's Consistent, Global Authorization System".

An instance of an `access-controller` is similar to the `aclserver` implementation called out in the paper. A cluster of access-controllers implement the functional equivalent of the Zanzibar `aclserver` cluster.

# Getting Started

## Start a Local Cluster
An access-controller server supports single node or multi-node (clustered) topologies. Instructions for running the server with these topologies are outlined below.

To gain the benefits of the distributed query model that the access-controller implements, it is recommend to run a large cluster. Doing so will help distribute query load across more nodes within the cluster. The underlying cluster membership list is based on Hashicorp's [`memberlist`](https://github.com/hashicorp/memberlist)

> a library that manages cluster membership and member failure detection using a gossip based protocol.

A cluster should be able to suport hundreds of nodes. If you find otherwise, please submit an issue.

### Binary

#### Single Node
```bash
$ ./access-controller
```

#### Multi-node
Start a multi-node cluster by starting multiple independent servers and use the `--join` flag
to join the node to an existing cluster.

```bash
$ ./access-controller --node-port 7946 --grpc-port 50052
$ ./access-controller --node-port 7947 --grpc-port 50053 --join 127.0.0.1:7946
$ ./access-controller --node-port 7948 --grpc-port 50054 --join 127.0.0.1:7947
```

### Kubernetes
A [Helm chart](./helm/access-controller) is included in this repository to provision an access-controller cluster in Kubernetes.

```bash
helm install access-controller ./helm/access-controller
```