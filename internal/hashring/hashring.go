package hashring

//go:generate mockgen -self_package github.com/authorizer-tech/access-controller/internal -destination=../mock_hashring_test.go -package accesscontroller . Hashring

import (
	"context"
	"hash/crc32"
	"sort"
	"strings"
	"sync"

	"github.com/buraksezer/consistent"
	"github.com/cespare/xxhash"
)

type ctxKey int

var hashringChecksumKey ctxKey

type hasher struct{}

func (h hasher) Sum64(data []byte) uint64 {
	return xxhash.Sum64(data)
}

// HashringMember represents an interface that types must implement to be a member of
// a Hashring.
type HashringMember interface {
	String() string
}

// Hashring defines an interface to manage a consistent hashring.
type Hashring interface {

	// Add adds a new member to the hashring.
	Add(member HashringMember)

	// Remove removes a member from the hashring.
	Remove(member HashringMember)

	// LocateKey finds the nearest hashring member for a given key.
	LocateKey(key []byte) HashringMember

	// Checksum computes the CRC32 checksum of the Hashring.
	//
	// This can be used to compare the relative state of two
	// hash rings on remote servers. If the checksum is the
	// same, then the two members can trust their memberlist
	// is identical. If not, then at some point in the future
	// the hashring memberlist should converge and then the
	// checksums will be identical.
	Checksum() uint32
}

// NewContextWithChecksum returns a new Context that carries the hashring checksum.
func NewContextWithChecksum(ctx context.Context, hashringChecksum uint32) context.Context {
	return context.WithValue(ctx, hashringChecksumKey, hashringChecksum)
}

// ChecksumFromContext extracts the hashring checksum from the provided
// ctx or returns false if none was found in the ctx.
func ChecksumFromContext(ctx context.Context) (uint32, bool) {
	checksum, ok := ctx.Value(hashringChecksumKey).(uint32)
	return checksum, ok
}

// ConsistentHashring implements a Hashring using consistent hashing with bounded loads.
type ConsistentHashring struct {
	rw   sync.RWMutex
	ring *consistent.Consistent
}

// NewConsistentHashring returns a Hashring using consistent hashing with bounded loads. The
// distribution of the load in the hashring is specified via the config provided. If the cfg
// is nil, defaults are used.
func NewConsistentHashring(cfg *consistent.Config) Hashring {

	if cfg == nil {
		cfg = &consistent.Config{
			Hasher:            &hasher{},
			PartitionCount:    31,
			ReplicationFactor: 3,
			Load:              1.25,
		}
	}

	hashring := &ConsistentHashring{
		ring: consistent.New(nil, *cfg),
	}
	return hashring
}

// Add adds the provided hashring member to the hashring
// memberlist.
func (ch *ConsistentHashring) Add(member HashringMember) {
	defer ch.rw.Unlock()
	ch.rw.Lock()
	ch.ring.Add(consistent.Member(member))
}

// Remove removes the provided hashring member from the hashring
// memberlist.
func (ch *ConsistentHashring) Remove(member HashringMember) {
	defer ch.rw.Unlock()
	ch.rw.Lock()
	ch.ring.Remove(member.String())
}

// LocateKey locates the nearest hashring member to the given
// key.
func (ch *ConsistentHashring) LocateKey(key []byte) HashringMember {
	defer ch.rw.RUnlock()
	ch.rw.RLock()
	return ch.ring.LocateKey(key)
}

// Checksum computes a consistent CRC32 checksum of the hashring
// members using the IEEE polynomial.
func (ch *ConsistentHashring) Checksum() uint32 {
	defer ch.rw.RUnlock()
	ch.rw.RLock()

	memberSet := make(map[string]struct{})
	for _, member := range ch.ring.GetMembers() {
		memberSet[member.String()] = struct{}{}
	}

	members := make([]string, 0, len(memberSet))
	for member := range memberSet {
		members = append(members, member)
	}

	sort.Strings(members)
	bytes := []byte(strings.Join(members, ","))
	return crc32.ChecksumIEEE(bytes)
}

// Always verify that we implement the interface
var _ Hashring = &ConsistentHashring{}
