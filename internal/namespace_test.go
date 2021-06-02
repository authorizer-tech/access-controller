package accesscontroller

import (
	"context"
	"reflect"
	"testing"
	"time"

	aclpb "github.com/authorizer-tech/access-controller/genprotos/authorizer/accesscontroller/v1alpha1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
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

func TestInMemPeerNamespaceConfigStore_SetNamespaceConfigSnapshot(t *testing.T) {

	store := &inmemPeerNamespaceConfigStore{
		configs: make(map[string]map[string]map[time.Time]*aclpb.NamespaceConfig),
	}

	timestamp1 := time.Now()
	timestamp2 := timestamp1.Add(1 * time.Minute)

	config1 := &aclpb.NamespaceConfig{
		Name: "namespace1",
	}
	config2 := &aclpb.NamespaceConfig{
		Name: "namespace2",
	}
	config3 := &aclpb.NamespaceConfig{
		Name: "namespace1",
		Relations: []*aclpb.Relation{
			{Name: "relation1"},
		},
	}

	// add a completely new peer and namespace config
	if err := store.SetNamespaceConfigSnapshot("peer1", config1.Name, config1, timestamp1); err != nil {
		t.Errorf("Expected nil error, but got '%v'", err)
	}

	// add a new namespace config for an existing peer
	if err := store.SetNamespaceConfigSnapshot("peer1", config2.Name, config2, timestamp1); err != nil {
		t.Errorf("Expected nil error, but got '%v'", err)
	}

	// add a new config snapshot for an existing namespace
	if err := store.SetNamespaceConfigSnapshot("peer1", config3.Name, config3, timestamp2); err != nil {
		t.Errorf("Expected nil error, but got '%v'", err)
	}

	cfg1, ok := store.configs["peer1"][config1.Name][timestamp1.Round(0).UTC()]
	if !ok {
		t.Errorf("Expected ok to be true, but got false")
	} else {
		if !proto.Equal(config1, cfg1) {
			t.Errorf("Expected namespace config '%v', but got '%v'", config1, cfg1)
		}
	}

	cfg2, ok := store.configs["peer1"][config2.Name][timestamp1.Round(0).UTC()]
	if !ok {
		t.Errorf("Expected ok to be true, but got false")
	} else {
		if !proto.Equal(config2, cfg2) {
			t.Errorf("Expected namespace config '%v', but got '%v'", config2, cfg2)
		}
	}

	cfg3, ok := store.configs["peer1"][config3.Name][timestamp2.Round(0).UTC()]
	if !ok {
		t.Errorf("Expected ok to be true, but got false")
	} else {
		if !proto.Equal(config3, cfg3) {
			t.Errorf("Expected namespace config '%v', but got '%v'", config3, cfg3)
		}
	}
}

func TestInMemPeerNamespaceConfigStore_ListNamespaceConfigSnapshots(t *testing.T) {

	type output struct {
		snapshots map[string]map[time.Time]*aclpb.NamespaceConfig
		err       error
	}

	timestamp := time.Now()
	config := &aclpb.NamespaceConfig{}

	store := &inmemPeerNamespaceConfigStore{
		configs: map[string]map[string]map[time.Time]*aclpb.NamespaceConfig{
			"peer1": {
				"namespace1": {
					timestamp: config,
				},
			},
			"peer2": {
				"namespace1": {
					timestamp: config,
				},
			},
		},
	}

	tests := []struct {
		name      string
		namespace string
		output    output
	}{
		{
			name:      "Test-1",
			namespace: "namespace1",
			output: output{
				snapshots: map[string]map[time.Time]*aclpb.NamespaceConfig{
					"peer1": {
						timestamp: config,
					},
					"peer2": {
						timestamp: config,
					},
				},
			},
		},
		{
			name:      "Test-2",
			namespace: "namespace2",
			output: output{
				snapshots: map[string]map[time.Time]*aclpb.NamespaceConfig{
					"peer1": {},
					"peer2": {},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshots, err := store.ListNamespaceConfigSnapshots(test.namespace)

			if err != test.output.err {
				t.Errorf("Expected error '%v', but got '%v'", test.output.err, err)
			} else {
				if !reflect.DeepEqual(test.output.snapshots, snapshots) {
					t.Errorf("Expected  '%v', but got '%v'", test.output.snapshots, snapshots)
				}
			}
		})
	}
}

func TestInMemPeerNamespaceConfigStore_GetNamespaceConfigSnapshot(t *testing.T) {

	type input struct {
		peerID    string
		namespace string
		timestamp time.Time
	}

	type output struct {
		config *aclpb.NamespaceConfig
		err    error
	}

	timestamp := time.Now()

	config1 := &aclpb.NamespaceConfig{
		Name: "namespace1",
	}

	store := &inmemPeerNamespaceConfigStore{
		configs: map[string]map[string]map[time.Time]*aclpb.NamespaceConfig{
			"peer1": {
				config1.Name: {
					timestamp: config1,
				},
			},
		},
	}

	tests := []struct {
		name   string
		input  input
		output output
	}{
		{
			name: "Test-1",
			input: input{
				peerID:    "peer1",
				namespace: "namespace1",
				timestamp: timestamp,
			},
			output: output{
				config: config1,
			},
		},
		{
			name: "Test-2",
			input: input{
				peerID: "peer2",
			},
			output: output{},
		},
		{
			name: "Test-3",
			input: input{
				peerID:    "peer1",
				namespace: "namespace2",
			},
			output: output{},
		},
		{
			name: "Test-4",
			input: input{
				peerID:    "peer1",
				namespace: "namespace1",
				timestamp: timestamp.Add(1 * time.Minute),
			},
			output: output{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg, err := store.GetNamespaceConfigSnapshot(test.input.peerID, test.input.namespace, test.input.timestamp)

			if err != test.output.err {
				t.Errorf("Expected error '%v', but got '%v'", test.output.err, err)
			} else {
				if !proto.Equal(test.output.config, cfg) {
					t.Errorf("Expected config '%v', but got '%v'", test.output.config, cfg)
				}
			}
		})
	}
}

func TestInMemPeerNamespaceConfigStore_GetNamespaceConfigSnapshots(t *testing.T) {

	type output struct {
		snapshots map[string]map[time.Time]*aclpb.NamespaceConfig
		err       error
	}

	timestamp := time.Now()

	config1 := &aclpb.NamespaceConfig{
		Name: "namespace1",
	}

	snapshots := map[string]map[time.Time]*aclpb.NamespaceConfig{
		config1.Name: {
			timestamp: config1,
		},
	}

	store := &inmemPeerNamespaceConfigStore{
		configs: map[string]map[string]map[time.Time]*aclpb.NamespaceConfig{
			"peer1": {
				config1.Name: {
					timestamp: config1,
				},
			},
		},
	}

	tests := []struct {
		name   string
		peerID string
		output output
	}{
		{
			name:   "Test-1",
			peerID: "peer1",
			output: output{
				snapshots: snapshots,
			},
		},
		{
			name:   "Test-2",
			peerID: "peer2",
			output: output{
				snapshots: map[string]map[time.Time]*aclpb.NamespaceConfig{},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshots, err := store.GetNamespaceConfigSnapshots(test.peerID)

			if err != test.output.err {
				t.Errorf("Expected error '%v', but got '%v'", test.output.err, err)
			} else {
				if !reflect.DeepEqual(test.output.snapshots, snapshots) {
					t.Errorf("Expected '%v', but got '%v'", test.output.snapshots, snapshots)
				}
			}
		})
	}
}

func TestInMemPeerNamespaceConfigStore_DeleteNamespaceConfigSnapshots(t *testing.T) {

	store := &inmemPeerNamespaceConfigStore{
		configs: make(map[string]map[string]map[time.Time]*aclpb.NamespaceConfig),
	}

	// deleting a peer that doesn't exist shouldn't panic
	if err := store.DeleteNamespaceConfigSnapshots("peer1"); err != nil {
		t.Errorf("Expected nil error, but got '%v'", err)
	}

	timestamp := time.Now()
	if err := store.SetNamespaceConfigSnapshot("peer2", "namespace1", &aclpb.NamespaceConfig{}, timestamp); err != nil {
		t.Fatalf("Failed to set namespace config snapshot")
	}

	if err := store.DeleteNamespaceConfigSnapshots("peer2"); err != nil {
		t.Errorf("Expected nil error, but got '%v'", err)
	}

	_, ok := store.configs["peer2"]["namespace1"][timestamp]
	if ok {
		t.Errorf("Expected ok to be false, but got true")
	}
}

func TestNamespaceConfigError_Error(t *testing.T) {

	err := NamespaceConfigError{
		Message: "some error",
	}

	if err.Error() != "some error" {
		t.Errorf("Expected error message 'some error', but got '%v'", err)
	}
}

func TestNamespaceConfigError_ToStatus(t *testing.T) {

	tests := []struct {
		name   string
		input  NamespaceConfigError
		output *status.Status
	}{
		{
			name: "Test-1",
			input: NamespaceConfigError{
				Message: "some error",
				Type:    NamespaceDoesntExist,
			},
			output: status.New(codes.InvalidArgument, "some error"),
		},
		{
			name: "Test-2",
			input: NamespaceConfigError{
				Message: "some error",
				Type:    NamespaceAlreadyExists,
			},
			output: status.New(codes.Unknown, "some error"),
		},
		{
			name: "Test-3",
			input: NamespaceConfigError{
				Message: "some error",
				Type:    NamespaceRelationUndefined,
			},
			output: status.New(codes.Unknown, "some error"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {

			status := test.input.ToStatus()

			if !reflect.DeepEqual(status, test.output) {
				t.Errorf("Expected status '%v', but got '%v'", test.output, status)
			}
		})
	}
}

type mockChangelogIterator struct {
	index int
	buf   []*NamespaceChangelogEntry
}

func NewMockChangelogIterator(changelog []*NamespaceChangelogEntry) ChangelogIterator {

	m := mockChangelogIterator{
		index: 0,
		buf:   changelog,
	}

	return &m
}

func (m *mockChangelogIterator) Next() bool {
	return m.index < len(m.buf)
}

func (m *mockChangelogIterator) Value() (*NamespaceChangelogEntry, error) {
	entry := m.buf[m.index]
	m.index += 1
	return entry, nil
}

func (m *mockChangelogIterator) Close(ctx context.Context) error {
	return nil
}
