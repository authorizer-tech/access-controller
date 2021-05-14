package accesscontroller

import (
	"context"
	"sync"
	"time"

	aclpb "github.com/authorizer-tech/access-controller/gen/go/authorizer-tech/accesscontroller/v1alpha1"
)

var nsConfigSnapshotTimestampKey ctxKey

// NewContextWithNamespaceConfigTimestamp returns a new Context that carries the namespace config timestamp.
func NewContextWithNamespaceConfigTimestamp(ctx context.Context, timestamp time.Time) context.Context {
	return context.WithValue(ctx, nsConfigSnapshotTimestampKey, timestamp)
}

// NamespaceConfigTimestampFromContext extracts the snapshot timestamp for a namespace
// configuration from the provided context. If none is present, a boolean false is returned.
func NamespaceConfigTimestampFromContext(ctx context.Context) (time.Time, bool) {
	timestamp, ok := ctx.Value(nsConfigSnapshotTimestampKey).(time.Time)
	return timestamp, ok
}

type NamespaceOperation string

const (
	UpdateNamespace NamespaceOperation = "UPDATE"
)

// NamespaceConfigSnapshot represents a namespace configuration at a specific point in time.
type NamespaceConfigSnapshot struct {
	Config    *aclpb.NamespaceConfig `json:"config"`
	Timestamp time.Time              `json:"timestamp"`
}

// ChangelogIterator is used to iterate over namespace changelog entries as they are yielded.
type ChangelogIterator interface {

	// Next prepares the next changelog entry for reading. It returns true
	// if there is another entry and false if no more entries are available.
	Next() bool

	// Value returns the current most changelog entry that the iterator is
	// iterating over.
	Value() (*NamespaceChangelogEntry, error)

	// Close closes the iterator.
	Close(ctx context.Context) error
}

// NamespaceChangelogEntry represents an entry in the namespace configurations
// changelog.
type NamespaceChangelogEntry struct {
	Namespace string
	Operation NamespaceOperation
	Config    *aclpb.NamespaceConfig
	Timestamp time.Time
}

// NamespaceManager defines an interface to manage/administer namespace configs.
type NamespaceManager interface {

	// WriteConfig appends the provided namespace configuration along with a timestamp
	// capturing the time at which the txn was committed.
	WriteConfig(ctx context.Context, cfg *aclpb.NamespaceConfig) error

	// GetConfig fetches the latest namespace config.
	GetConfig(ctx context.Context, namespace string) (*aclpb.NamespaceConfig, error)

	// GetRewrite fetches the rewrite rule for the given (namespace, relation) tuple using
	// the latest namespace config available.
	GetRewrite(ctx context.Context, namespace, relation string) (*aclpb.Rewrite, error)

	// TopChanges returns the top n most recent changes for each namespace configuration(s).
	//
	// For example, suppose you have M number of namespace configs and each config has X
	// number of snapshots. This yields an iterator that will iterate over at most M * n
	// values. If n >= X, then the iterator will iterate over at most M * X values.
	TopChanges(ctx context.Context, n uint) (ChangelogIterator, error)
}

// PeerNamespaceConfigStore defines the interface to store the namespace config snapshots
// for each peer within a cluster.
type PeerNamespaceConfigStore interface {

	// SetNamespaceConfigSnapshot stores the namespace config snapshot for the given peer.
	SetNamespaceConfigSnapshot(peerID string, namespace string, config *aclpb.NamespaceConfig, ts time.Time) error

	// ListNamespaceConfigSnapshots returns a map whose keys are the peers and whose values are another
	// map storing the timestamps of the snapshots for each namespace config.
	ListNamespaceConfigSnapshots(namespace string) (map[string]map[time.Time]*aclpb.NamespaceConfig, error)

	// GetNamespaceConfigSnapshots returns a map of namespaces and that namespace's snapshot configs for
	// a given peer.
	GetNamespaceConfigSnapshots(peerID string) (map[string]map[time.Time]*aclpb.NamespaceConfig, error)

	// GetNamespaceConfigSnapshot returns the specific namespace config for a peer from the snapshot timestamp
	// provided, or nil if one didn't exist at that point in time.
	GetNamespaceConfigSnapshot(peerID, namespace string, timestamp time.Time) (*aclpb.NamespaceConfig, error)

	// DeleteNamespaceConfigSnapshots deletes all of the namespaces configs for the given peer.
	DeleteNamespaceConfigSnapshots(peerID string) error
}

type inmemPeerNamespaceConfigStore struct {
	rwmu    sync.RWMutex
	configs map[string]map[string]map[time.Time]*aclpb.NamespaceConfig
}

func (p *inmemPeerNamespaceConfigStore) SetNamespaceConfigSnapshot(peerID string, namespace string, config *aclpb.NamespaceConfig, ts time.Time) error {
	p.rwmu.Lock()
	defer p.rwmu.Unlock()

	namespaceConfigs, ok := p.configs[peerID]
	if !ok {
		// key doesn't exist yet, write it!
		p.configs[peerID] = map[string]map[time.Time]*aclpb.NamespaceConfig{
			namespace: {
				ts: config,
			},
		}
	} else {
		// key exists, update the underlying map
		snapshots, ok := namespaceConfigs[namespace]
		if !ok {
			namespaceConfigs[namespace] = map[time.Time]*aclpb.NamespaceConfig{
				ts: config,
			}
		} else {
			snapshots[ts] = config
		}
	}

	return nil
}

func (p *inmemPeerNamespaceConfigStore) ListNamespaceConfigSnapshots(namespace string) (map[string]map[time.Time]*aclpb.NamespaceConfig, error) {
	p.rwmu.RLock()
	defer p.rwmu.RUnlock()

	peerSnapshots := map[string]map[time.Time]*aclpb.NamespaceConfig{}
	for peer, configs := range p.configs {
		snapshots, ok := configs[namespace]
		if !ok {
			// peer isn't aware of the namespace yet
			peerSnapshots[peer] = map[time.Time]*aclpb.NamespaceConfig{}
		} else {
			peerSnapshots[peer] = snapshots
		}
	}

	return peerSnapshots, nil
}

func (p *inmemPeerNamespaceConfigStore) GetNamespaceConfigSnapshot(peerID, namespace string, ts time.Time) (*aclpb.NamespaceConfig, error) {
	p.rwmu.RLock()
	defer p.rwmu.RUnlock()

	configs, ok := p.configs[peerID]
	if !ok {
		return nil, nil
	}

	snapshots, ok := configs[namespace]
	if !ok {
		return nil, nil
	}

	return snapshots[ts], nil
}

func (p *inmemPeerNamespaceConfigStore) GetNamespaceConfigSnapshots(peerID string) (map[string]map[time.Time]*aclpb.NamespaceConfig, error) {
	p.rwmu.RLock()
	defer p.rwmu.RUnlock()

	configs, ok := p.configs[peerID]
	if !ok {
		return nil, nil
	}

	return configs, nil
}

func (p *inmemPeerNamespaceConfigStore) DeleteNamespaceConfigSnapshots(peerID string) error {
	p.rwmu.Lock()
	defer p.rwmu.Unlock()

	delete(p.configs, peerID)

	return nil
}
