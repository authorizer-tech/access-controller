package accesscontroller

import (
	"context"
	"testing"

	"github.com/cespare/xxhash"
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
