package accesscontroller

//go:generate mockgen -self_package github.com/authorizer-tech/access-controller/internal -destination=./mock_namespace_manager_test.go -package accesscontroller . NamespaceManager,PeerNamespaceConfigStore

import (
	"context"
	"errors"
	"sync"
	"time"

	aclpb "github.com/authorizer-tech/access-controller/genprotos/authorizer/accesscontroller/v1alpha1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// NamespaceConfigErrorType defines an enumeration over various types of Namespace Config
// errors that can occur.
type NamespaceConfigErrorType int

const (

	// NamespaceAlreadyExists is an error that occurrs when attempting to add a namespace config
	// for a namespace that has been previously added.
	NamespaceAlreadyExists NamespaceConfigErrorType = iota

	// NamespaceDoesntExist is an error that occurrs when attempting to fetch a namespace config
	// for a namespace that doesn't exist.
	NamespaceDoesntExist

	// NamespaceRelationUndefined is an error that occurrs when referencing an undefined relation
	// in a namespace config.
	NamespaceRelationUndefined

	// NamespaceUpdateFailedPrecondition is an error that occurrs when an update to a namespace config
	// fails precondition checks.
	NamespaceUpdateFailedPrecondition
)

// NamespaceConfigError represents an error type that is surfaced when Namespace Config
// errors are encountered.
type NamespaceConfigError struct {
	Message string
	Type    NamespaceConfigErrorType
}

// Error returns the NamespaceConfigError as an error string.
func (e NamespaceConfigError) Error() string {
	return e.Message
}

// ToStatus returns the namespace config error as a grpc status.
func (e NamespaceConfigError) ToStatus() *status.Status {
	switch e.Type {
	case NamespaceDoesntExist, NamespaceUpdateFailedPrecondition, NamespaceRelationUndefined:
		return status.New(codes.InvalidArgument, e.Message)
	default:
		return status.New(codes.Unknown, e.Message)
	}
}

// ErrNamespaceDoesntExist is an error that occurrs when attempting to fetch a namespace config
// for a namespace that doesn't exist.
var ErrNamespaceDoesntExist error = errors.New("the provided namespace doesn't exist, please add it first")

// ErrNoLocalNamespacesDefined is an error that occurrs when attempting to fetch a namespace config
// and no local namespaces have been defined.
var ErrNoLocalNamespacesDefined error = errors.New("no local namespace configs have been defined at this time")

type nsConfigSnapshotTimestampKey string

// NewContextWithNamespaceConfigTimestamp returns a new Context that carries the namespace config name and timestamp.
func NewContextWithNamespaceConfigTimestamp(ctx context.Context, namespace string, timestamp time.Time) context.Context {
	return context.WithValue(ctx, nsConfigSnapshotTimestampKey(namespace), timestamp)
}

// NamespaceConfigTimestampFromContext extracts the snapshot timestamp for a namespace
// configuration from the provided context. If none is present, a boolean false is returned.
func NamespaceConfigTimestampFromContext(ctx context.Context, namespace string) (time.Time, bool) {
	timestamp, ok := ctx.Value(nsConfigSnapshotTimestampKey(namespace)).(time.Time)
	return timestamp, ok
}

// NamespaceOperation represents the operations that can be taken on namespace configs.
type NamespaceOperation string

const (

	// AddNamespace is the operation when a new namespace config is added.
	AddNamespace NamespaceOperation = "ADD"

	// UpdateNamespace is the operation when a namespace config is updated.
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

	// UpsertConfig upserts the provided namespace configuration along with a timestamp
	// capturing the time at which the txn was committed.
	UpsertConfig(ctx context.Context, cfg *aclpb.NamespaceConfig) error

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

	// LookupRelationReferencesByCount does a reverse lookup by the (namespace, relation...) pairs
	// and returns a map whose keys are the relations and whose values indicate the number of relation
	// tuples that reference the (namespace, relation) pair. If a (namespace, relation) pair is not
	// referenced at all, it's key is omitted in the output map.
	LookupRelationReferencesByCount(ctx context.Context, namespace string, relations ...string) (map[string]int, error)

	// WrapTransaction wraps the provided fn in a single transaction using the context.
	// If fn returns an error the transaction is rolled back and aborted. Otherwise it
	// is committed.
	WrapTransaction(ctx context.Context, fn func(ctx context.Context) error) error
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

	// To guarantee map key equality, time values must have identical Locations
	// and the monotonic clock reading should be stripped.
	ts = ts.Round(0).UTC()

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
		return map[string]map[time.Time]*aclpb.NamespaceConfig{}, nil
	}

	return configs, nil
}

func (p *inmemPeerNamespaceConfigStore) DeleteNamespaceConfigSnapshots(peerID string) error {
	p.rwmu.Lock()
	defer p.rwmu.Unlock()

	delete(p.configs, peerID)

	return nil
}
