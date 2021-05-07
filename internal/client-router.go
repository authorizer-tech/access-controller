package accesscontroller

import (
	"fmt"
	"sync"
)

var ErrClientNotFound = fmt.Errorf("The client with the provided identifier was not found")

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
