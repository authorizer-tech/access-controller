package accesscontroller

//go:generate mockgen -self_package github.com/authorizer-tech/access-controller/internal -destination=./mock_clientrouter_test.go -package accesscontroller . ClientRouter

import (
	"fmt"
	"sync"
)

// ErrClientNotFound is an error that occurrs if a ClientRouter.GetClient call is
// invoked with a nodeID that does not exist.
var ErrClientNotFound = fmt.Errorf("the client with the provided identifier was not found")

// ClientRouter defines an interface to manage RPC clients for nodes/peers
// within a cluster.
type ClientRouter interface {
	AddClient(nodeID string, client interface{})
	GetClient(nodeID string) (interface{}, error)
	RemoveClient(nodeID string)
}

// mapClientRouter implements the ClientRouter interface ontop of a simple
// map structure.
type mapClientRouter struct {
	rw    sync.RWMutex
	cache map[string]interface{}
}

// NewMapClientRouter creates an in-memory, map based, ClientRouter. It is
// safe for concurrent use.
func NewMapClientRouter() ClientRouter {
	r := mapClientRouter{
		cache: map[string]interface{}{},
	}

	return &r
}

// AddClient adds the client for the given nodeID to the underlying
// map cache.
//
// This method is safe for concurrent use.
func (r *mapClientRouter) AddClient(nodeID string, client interface{}) {
	defer r.rw.Unlock()
	r.rw.Lock()
	r.cache[nodeID] = client
}

// GetClient fetches the client for the given nodeID or returns an
// error if none exists.
//
// This method is safe for concurrent use.
func (r *mapClientRouter) GetClient(nodeID string) (interface{}, error) {
	defer r.rw.RUnlock()
	r.rw.RLock()

	client, ok := r.cache[nodeID]
	if !ok {
		return nil, ErrClientNotFound
	}
	return client, nil
}

// RemoveClient removes the client for the given nodeID from the map
// cache.
//
// This method is safe for concurrent use.
func (r *mapClientRouter) RemoveClient(nodeID string) {
	defer r.rw.Unlock()
	r.rw.Lock()
	delete(r.cache, nodeID)
}

// Always verify that we implement the interface
var _ ClientRouter = &mapClientRouter{}
