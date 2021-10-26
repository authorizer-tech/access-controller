package hashring

import (
	"context"
	"hash/crc32"
	"sort"
	"strings"
	"testing"

	"github.com/cespare/xxhash"
	"github.com/hashicorp/memberlist"
)

func TestHasher_Sum64(t *testing.T) {

	sum := hasher{}.Sum64([]byte("test"))
	expected := xxhash.Sum64([]byte("test"))

	if sum != expected {
		t.Errorf("Expected '%v', but got '%v'", expected, sum)
	}
}

func TestChecksumFromContext(t *testing.T) {

	type output struct {
		checksum uint32
		ok       bool
	}

	tests := []struct {
		input  context.Context
		output output
	}{
		{
			input: context.Background(),
			output: output{
				ok: false,
			},
		},
		{
			input: NewContextWithChecksum(context.Background(), 1),
			output: output{
				checksum: 1,
				ok:       true,
			},
		},
	}

	for _, test := range tests {
		checksum, ok := ChecksumFromContext(test.input)

		if ok != test.output.ok {
			t.Errorf("Expected ok to be '%v', but got '%v'", test.output.ok, ok)
		} else {
			if checksum != test.output.checksum {
				t.Errorf("Expected timestamp '%v', but got '%v'", test.output.checksum, checksum)
			}
		}
	}
}

func TestConsistentHashring_Remove(t *testing.T) {

	node1 := &memberlist.Node{Name: "node1"}
	node2 := &memberlist.Node{Name: "node2"}

	ring := NewConsistentHashring(nil)

	ring.Add(node1)
	ring.Add(node2)

	checksum1 := checksum(node1, node2)
	if checksum1 != ring.Checksum() {
		t.Errorf("Expected checksum '%d', but got '%d'", ring.Checksum(), checksum1)
	}

	ring.Remove(node2)
	checksum2 := checksum(node1)
	if checksum2 != ring.Checksum() {
		t.Errorf("Expected checksum '%d', but got '%d'", ring.Checksum(), checksum1)
	}

	if expected := crc32.ChecksumIEEE([]byte(node1.String())); checksum2 != expected {
		t.Errorf("Expected checksum '%d', but got '%d'", expected, checksum2)
	}
}

func TestConsistentHashring_LocateKey(t *testing.T) {

	node1 := &memberlist.Node{Name: "node1"}
	node2 := &memberlist.Node{Name: "node2"}

	ring := NewConsistentHashring(nil)

	ring.Add(node1)

	member := ring.LocateKey([]byte("key1")).String()
	if member != "node1" {
		t.Errorf("Expected '%s', but got '%s'", "node1", member)
	}

	ring.Add(node2)

	member = ring.LocateKey([]byte("key1")).String()
	if member != "node2" {
		t.Errorf("Expected '%s', but got '%s'", "node2", member)
	}
}

func TestConsistentHashring_Checksum(t *testing.T) {

	node1 := &memberlist.Node{Name: "node1"}
	node2 := &memberlist.Node{Name: "node2"}

	ring := NewConsistentHashring(nil)
	ring.Add(node2)
	ring.Add(node1)

	expected := checksum(node1, node2)

	if expected != ring.Checksum() {
		t.Errorf("Expected checksum '%d', but got '%d'", ring.Checksum(), expected)
	}
}

func checksum(members ...*memberlist.Node) uint32 {

	memberSet := make(map[string]struct{})
	for _, member := range members {
		memberSet[member.String()] = struct{}{}
	}

	m := make([]string, 0, len(memberSet))
	for member := range memberSet {
		m = append(m, member)
	}

	sort.Strings(m)
	bytes := []byte(strings.Join(m, ","))

	return crc32.ChecksumIEEE(bytes)
}
