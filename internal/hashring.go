package accesscontroller

import (
	"context"
	"hash/crc32"
	"sort"
	"strings"
	"sync"

	"github.com/buraksezer/consistent"
)

type ctxKey int

var hashringChecksumKey ctxKey

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
	LocateKey(key []byte) string

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

// NewContext returns a new Context that carries the hash ring checksum.
func NewContext(ctx context.Context, hashringChecksum uint32) context.Context {
	return context.WithValue(ctx, hashringChecksumKey, hashringChecksum)
}

func FromContext(ctx context.Context) (uint32, bool) {
	checksum, ok := ctx.Value(hashringChecksumKey).(uint32)
	return checksum, ok
}

type ConsistentHashring struct {
	rw   sync.RWMutex
	Ring *consistent.Consistent
}

// Add adds the provided hashring member to the hashring
// memberlist.
func (ch *ConsistentHashring) Add(member HashringMember) {
	defer ch.rw.Unlock()
	ch.rw.Lock()
	ch.Ring.Add(consistent.Member(member))
}

// Remove removes the provided hashring member from the hashring
// memberlist.
func (ch *ConsistentHashring) Remove(member HashringMember) {
	defer ch.rw.Unlock()
	ch.rw.Lock()
	ch.Ring.Remove(member.String())
}

// LocateKey locates the nearest hashring member to for the given
// key.
func (ch *ConsistentHashring) LocateKey(key []byte) string {
	defer ch.rw.RUnlock()
	ch.rw.RLock()
	return ch.Ring.LocateKey(key).String()
}

// Checksum computes a consistent CRC32 checksum of the hashring
// members using the IEEE polynomial.
func (ch *ConsistentHashring) Checksum() uint32 {
	defer ch.rw.RUnlock()
	ch.rw.RLock()

	memberSet := make(map[string]struct{})
	for _, member := range ch.Ring.GetMembers() {
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
