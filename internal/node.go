package accesscontroller

import (
	"encoding/json"
	"fmt"
	"sync"

	aclpb "github.com/authorizer-tech/access-controller/gen/go/authorizer-tech/accesscontroller/v1alpha1"
	"github.com/hashicorp/memberlist"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
)

// Node represents a single node in a cluster. It contains the list
// of other members in the cluster and clients for communicating with
// other nodes.
type Node struct {
	rw sync.RWMutex

	// The unique identifier of the node within the cluster.
	ID string

	Memberlist *memberlist.Memberlist
	RpcRouter  ClientRouter
	Hashring   Hashring
}

type NodeMetadata struct {
	Port int `json:"port"`
}

// NotifyJoin is invoked when a new node has joined the cluster.
// The `member` argument must not be modified.
func (n *Node) NotifyJoin(member *memberlist.Node) {

	log.Infof("Cluster member with id '%s' joined the cluster at address '%s'", member.String(), member.FullAddress().Addr)

	nodeID := member.String()
	if nodeID != n.ID {
		var meta NodeMetadata
		if err := json.Unmarshal(member.Meta, &meta); err != nil {
			// todo: handle error better
			log.Errorf("Failed to json.Unmarshal the Node metadata: %v", err)
		}

		remoteAddr := fmt.Sprintf("%s:%d", member.Addr, meta.Port)

		opts := []grpc.DialOption{
			grpc.WithInsecure(),
		}
		conn, err := grpc.Dial(remoteAddr, opts...)
		if err != nil {
			// todo: handle error better
			log.Errorf("Failed to establish a grpc connection to cluster member '%s' at address '%s'", nodeID, remoteAddr)
			return
		}

		client := aclpb.NewCheckServiceClient(conn)

		n.RpcRouter.AddClient(nodeID, client)
	}

	n.Hashring.Add(member)
	log.Tracef("hashring checksum: %d", n.Hashring.Checksum())
}

// NotifyLeave is invoked when a node leaves the cluster. The
// `member` argument must not be modified.
func (n *Node) NotifyLeave(member *memberlist.Node) {

	log.Infof("Cluster member with id '%v' at address '%v' left the cluster", member.String(), member.FullAddress().Addr)

	nodeID := member.String()
	if nodeID != n.ID {
		n.RpcRouter.RemoveClient(nodeID)
	}

	n.Hashring.Remove(member)
	log.Tracef("hashring checksum: %d", n.Hashring.Checksum())
}

// NotifyUpdate is invoked when a node in the cluster is updated,
// usually involving the meta-data of the node. The `member` argument
// must not be modified.
func (n *Node) NotifyUpdate(member *memberlist.Node) {}
