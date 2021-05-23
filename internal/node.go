package accesscontroller

import (
	"encoding/json"
	"fmt"
	"time"

	aclpb "github.com/authorizer-tech/access-controller/genprotos/authorizer/accesscontroller/v1alpha1"
	"github.com/hashicorp/memberlist"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
)

// NodeConfigs represent the configurations for an individual node withn a gossip
// cluster.
type NodeConfigs struct {

	// A unique identifier for this node in the cluster.
	ServerID string

	// The address used to advertise to other cluster members. Used
	// for nat traversal.
	Advertise string

	// A comma-separated list of existing nodes in the cluster to
	// join this node to.
	Join string

	// The port that cluster membership gossip is occurring on.
	NodePort int

	// The port serving the access-controller RPCs.
	ServerPort int
}

// NodeMetadata is local data specific to this node within the cluster. The
// node's metadata is broadcasted periodically to all peers/nodes in the cluster.
type NodeMetadata struct {
	NodeID                   string                                          `json:"node-id"`
	ServerPort               int                                             `json:"port"`
	NamespaceConfigSnapshots map[string]map[time.Time]*aclpb.NamespaceConfig `json:"namespace-snapshots"`
}

// Node represents a single node in a cluster. It contains the list
// of other members in the cluster and clients for communicating with
// other nodes.
type Node struct {

	// The unique identifier of the node within the cluster.
	ID string

	Memberlist *memberlist.Memberlist
	RPCRouter  ClientRouter
	Hashring   Hashring
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

		remoteAddr := fmt.Sprintf("%s:%d", member.Addr, meta.ServerPort)

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

		n.RPCRouter.AddClient(nodeID, client)
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
		n.RPCRouter.RemoveClient(nodeID)
	}

	n.Hashring.Remove(member)
	log.Tracef("hashring checksum: %d", n.Hashring.Checksum())

	err := peerNamespaceConfigs.DeleteNamespaceConfigSnapshots(nodeID)
	if err != nil {
		log.Errorf("Failed to delete namespace config snapshots for peer with id '%s'", nodeID)
	}
}

// NotifyUpdate is invoked when a node in the cluster is updated,
// usually involving the meta-data of the node. The `member` argument
// must not be modified.
func (n *Node) NotifyUpdate(member *memberlist.Node) {}
