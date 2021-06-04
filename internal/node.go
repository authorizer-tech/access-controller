package accesscontroller

import (
	"time"
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
	NodeID     string `json:"node-id"`
	ServerPort int    `json:"port"`

	// A map of namespace config snapshots in their JSON serialized form.
	NamespaceConfigSnapshots map[string]map[time.Time][]byte `json:"namespace-snapshots"`
}
