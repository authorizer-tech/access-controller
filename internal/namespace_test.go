package accesscontroller

import (
	"context"
	"testing"
	"time"
)

func TestNamespaceConfigTimestampFromContext(t *testing.T) {

	type output struct {
		timestamp time.Time
		ok        bool
	}

	ts := time.Now()

	tests := []struct {
		input  context.Context
		output output
	}{
		{
			input: context.Background(),
			output: output{
				timestamp: time.Time{},
				ok:        false,
			},
		},
		{
			input: NewContextWithNamespaceConfigTimestamp(context.Background(), ts),
			output: output{
				timestamp: ts,
				ok:        true,
			},
		},
	}

	for _, test := range tests {
		timestamp, ok := NamespaceConfigTimestampFromContext(test.input)

		if ok != test.output.ok {
			t.Errorf("Expected ok to be '%v', but got '%v'", test.output.ok, ok)
		} else {
			if timestamp != test.output.timestamp {
				t.Errorf("Expected timestamp '%v', but got '%v'", test.output.timestamp, timestamp)
			}
		}
	}
}
