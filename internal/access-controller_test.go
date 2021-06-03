package accesscontroller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	reflect "reflect"
	"testing"
	"time"

	aclpb "github.com/authorizer-tech/access-controller/genprotos/authorizer/accesscontroller/v1alpha1"
	"github.com/golang/mock/gomock"
	"github.com/hashicorp/memberlist"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestAccessController_check(t *testing.T) {

	dbError := errors.New("database error")

	timestamp1 := time.Now()

	groupsConfig := &aclpb.NamespaceConfig{
		Name: "groups",
		Relations: []*aclpb.Relation{
			{Name: "member"},
		},
	}

	dirsConfig := &aclpb.NamespaceConfig{
		Name: "dirs",
		Relations: []*aclpb.Relation{
			{Name: "viewer"},
		},
	}

	filesConfig := &aclpb.NamespaceConfig{
		Name: "files",
		Relations: []*aclpb.Relation{
			{
				Name: "owner",
			},
			{
				Name: "editor",
				Rewrite: &aclpb.Rewrite{
					RewriteOperation: &aclpb.Rewrite_Intersection{
						Intersection: &aclpb.SetOperation{
							Children: []*aclpb.SetOperation_Child{
								{ChildType: &aclpb.SetOperation_Child_Rewrite{
									Rewrite: &aclpb.Rewrite{
										RewriteOperation: &aclpb.Rewrite_Union{
											Union: &aclpb.SetOperation{
												Children: []*aclpb.SetOperation_Child{
													{ChildType: &aclpb.SetOperation_Child_This_{
														This: &aclpb.SetOperation_Child_This{},
													}},
													{ChildType: &aclpb.SetOperation_Child_ComputedSubjectset{
														ComputedSubjectset: &aclpb.ComputedSubjectset{Relation: "owner"},
													}},
												},
											},
										},
									},
								}},
							},
						},
					},
				},
			},
			{
				Name: "viewer",
				Rewrite: &aclpb.Rewrite{
					RewriteOperation: &aclpb.Rewrite_Union{
						Union: &aclpb.SetOperation{
							Children: []*aclpb.SetOperation_Child{
								{
									ChildType: &aclpb.SetOperation_Child_TupleToSubjectset{
										TupleToSubjectset: &aclpb.TupleToSubjectset{
											Tupleset: &aclpb.TupleToSubjectset_Tupleset{
												Relation: "parent",
											},
											ComputedSubjectset: &aclpb.ComputedSubjectset{
												Relation: "viewer",
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	type input struct {
		ctx       context.Context
		namespace string
		object    string
		relation  string
		subject   string
	}

	type output struct {
		allowed bool
		err     error
	}

	tests := []struct {
		name string
		input
		output
		mockController func(mstore *MockRelationTupleStore)
	}{
		{
			name: "Test-1: Direct ACL without rewrite allowed",
			input: input{
				ctx:       context.Background(),
				namespace: "groups",
				object:    "group1",
				relation:  "member",
				subject:   "subject1",
			},
			output: output{
				allowed: true,
			},
			mockController: func(mstore *MockRelationTupleStore) {
				mstore.EXPECT().RowCount(gomock.Any(), RelationTupleQuery{
					Object: Object{
						Namespace: "groups",
						ID:        "group1",
					},
					Relations: []string{"member"},
					Subject:   &SubjectID{ID: "subject1"},
				}).Return(int64(1), nil)
			},
		},
		{
			name: "Test-2: No errors and not allowed",
			input: input{
				ctx:       context.Background(),
				namespace: "groups",
				object:    "group1",
				relation:  "member",
				subject:   "subject1",
			},
			output: output{
				allowed: false,
			},
			mockController: func(mstore *MockRelationTupleStore) {
				gomock.InOrder(
					mstore.EXPECT().RowCount(gomock.Any(), gomock.Any()).Return(int64(0), nil),

					mstore.EXPECT().SubjectSets(gomock.Any(), Object{
						Namespace: "groups",
						ID:        "group1",
					}, "member").Return([]SubjectSet{}, nil),
				)
			},
		},
		{
			name: "Test-3: RelationTupleStore.RowCount error",
			input: input{
				ctx:       context.Background(),
				namespace: "groups",
				object:    "group1",
				relation:  "member",
				subject:   "subject1",
			},
			output: output{
				allowed: false,
				err:     dbError,
			},
			mockController: func(mstore *MockRelationTupleStore) {
				mstore.EXPECT().RowCount(gomock.Any(), gomock.Any()).Return(int64(-1), dbError)
			},
		},
		{
			name: "Test-4: Nested Rewrites with allowed outcome",
			input: input{
				ctx:       context.Background(),
				namespace: "files",
				object:    "file1",
				relation:  "editor",
				subject:   "subject1",
			},
			output: output{
				allowed: true,
			},
			mockController: func(mstore *MockRelationTupleStore) {

				mstore.EXPECT().RowCount(gomock.Any(), RelationTupleQuery{
					Object: Object{
						Namespace: "files",
						ID:        "file1",
					},
					Relations: []string{"editor"},
					Subject:   &SubjectID{ID: "subject1"},
				}).Return(int64(0), nil)

				mstore.EXPECT().RowCount(gomock.Any(), RelationTupleQuery{
					Object: Object{
						Namespace: "files",
						ID:        "file1",
					},
					Relations: []string{"owner"},
					Subject:   &SubjectID{ID: "subject1"},
				}).Return(int64(1), nil)

				mstore.EXPECT().SubjectSets(gomock.Any(), Object{
					Namespace: "files",
					ID:        "file1",
				}, "editor").Return([]SubjectSet{}, nil)
			},
		},
		{
			name: "Test-5: SubjectSet Indirection Followed",
			input: input{
				ctx:       context.Background(),
				namespace: "groups",
				object:    "group1",
				relation:  "member",
				subject:   "subject1",
			},
			output: output{
				allowed: true,
			},
			mockController: func(mstore *MockRelationTupleStore) {

				mstore.EXPECT().RowCount(gomock.Any(), RelationTupleQuery{
					Object: Object{
						Namespace: "groups",
						ID:        "group1",
					},
					Relations: []string{"member"},
					Subject:   &SubjectID{ID: "subject1"},
				}).Return(int64(0), nil)

				mstore.EXPECT().RowCount(gomock.Any(), RelationTupleQuery{
					Object: Object{
						Namespace: "groups",
						ID:        "group2",
					},
					Relations: []string{"member"},
					Subject:   &SubjectID{ID: "subject1"},
				}).Return(int64(1), nil)

				mstore.EXPECT().SubjectSets(gomock.Any(), Object{
					Namespace: "groups",
					ID:        "group1",
				}, "member").Return([]SubjectSet{
					{
						Namespace: "groups",
						Object:    "group2",
						Relation:  "member",
					},
				}, nil)
			},
		},
		{
			name: "Test-6: TupleToSubjectSet Indirection Followed",
			input: input{
				ctx:       context.Background(),
				namespace: "files",
				object:    "file1",
				relation:  "viewer",
				subject:   "subject1",
			},
			output: output{
				allowed: true,
			},
			mockController: func(mstore *MockRelationTupleStore) {

				mstore.EXPECT().RowCount(gomock.Any(), RelationTupleQuery{
					Object: Object{
						Namespace: "dirs",
						ID:        "dir1",
					},
					Relations: []string{"viewer"},
					Subject:   &SubjectID{ID: "subject1"},
				}).Return(int64(1), nil)

				mstore.EXPECT().SubjectSets(gomock.Any(), Object{
					Namespace: "files",
					ID:        "file1",
				}, "parent").Return([]SubjectSet{
					{
						Namespace: "dirs",
						Object:    "dir1",
						Relation:  "...",
					},
				}, nil)
			},
		},
		{
			name: "Test-7: TupleToSubjectSet with allowed=false outcome",
			input: input{
				ctx:       context.Background(),
				namespace: "files",
				object:    "file1",
				relation:  "viewer",
				subject:   "subject1",
			},
			output: output{
				allowed: false,
			},
			mockController: func(mstore *MockRelationTupleStore) {

				mstore.EXPECT().SubjectSets(gomock.Any(), Object{
					Namespace: "files",
					ID:        "file1",
				}, "parent").Return([]SubjectSet{}, nil)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {

			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockStore := NewMockRelationTupleStore(ctrl)
			mockNamespaceManager := NewMockNamespaceManager(ctrl)

			if test.mockController != nil {
				test.mockController(mockStore)
			}

			mockNamespaceManager.EXPECT().TopChanges(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(func(ctx context.Context, n uint) (ChangelogIterator, error) {
				changelog := []*NamespaceChangelogEntry{
					{
						Namespace: groupsConfig.Name,
						Operation: AddNamespace,
						Config:    groupsConfig,
						Timestamp: timestamp1,
					},
					{
						Namespace: filesConfig.Name,
						Operation: AddNamespace,
						Config:    filesConfig,
						Timestamp: timestamp1,
					},
					{
						Namespace: dirsConfig.Name,
						Operation: AddNamespace,
						Config:    dirsConfig,
						Timestamp: timestamp1,
					},
				}
				iter := NewMockChangelogIterator(changelog)

				return iter, nil
			}).AnyTimes()

			opts := []AccessControllerOption{
				WithStore(mockStore),
				WithNamespaceManager(mockNamespaceManager),
			}

			controller, err := NewAccessController(opts...)
			if err != nil {
				t.Fatalf("Failed to initialize the AccessController: %v", err)
			}

			allowed, err := controller.check(test.input.ctx, test.input.namespace, test.input.object, test.input.relation, test.input.subject)

			if !errors.Is(err, test.output.err) {
				t.Errorf("Expected error '%v', but got '%v'", err, test.output.err)
			}

			if allowed != test.output.allowed {
				t.Errorf("Expected '%v', but got '%v'", test.output.allowed, allowed)
			}
		})
	}
}

var namespace1Config = &aclpb.NamespaceConfig{
	Name: "namespace1",
	Relations: []*aclpb.Relation{
		{Name: "relation1"},
	},
}

var namespace2Config = &aclpb.NamespaceConfig{
	Name: "namespace2",
	Relations: []*aclpb.Relation{
		{Name: "relation1"},
	},
}

func TestAccessController_WriteRelationTuplesTxn(t *testing.T) {

	timestamp1 := time.Now()

	subjectSet := &SubjectSet{
		Namespace: namespace2Config.Name,
		Object:    "object2",
		Relation:  "relation2",
	}

	type output struct {
		response *aclpb.WriteRelationTuplesTxnResponse
		err      error
	}

	tests := []struct {
		name  string
		input *aclpb.WriteRelationTuplesTxnRequest
		output
		mockController func(store *MockRelationTupleStore, nsmanager *MockNamespaceManager)
	}{
		{
			name: "Test-1",
			input: &aclpb.WriteRelationTuplesTxnRequest{
				RelationTupleDeltas: []*aclpb.RelationTupleDelta{
					{
						Action: aclpb.RelationTupleDelta_ACTION_INSERT,
						RelationTuple: &aclpb.RelationTuple{
							Namespace: namespace1Config.Name,
							Object:    "object1",
							Relation:  "relation1",
							Subject: &aclpb.Subject{
								Ref: &aclpb.Subject_Id{Id: "subject1"},
							},
						},
					},
					{
						Action: aclpb.RelationTupleDelta_ACTION_DELETE,
						RelationTuple: &aclpb.RelationTuple{
							Namespace: namespace1Config.Name,
							Object:    "object2",
							Relation:  "relation1",
							Subject: &aclpb.Subject{
								Ref: &aclpb.Subject_Id{Id: "subject2"},
							},
						},
					},
				},
			},
			output: output{
				response: &aclpb.WriteRelationTuplesTxnResponse{},
			},
			mockController: func(store *MockRelationTupleStore, nsmanager *MockNamespaceManager) {
				nsmanager.EXPECT().TopChanges(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(func(ctx context.Context, n uint) (ChangelogIterator, error) {
					changelog := []*NamespaceChangelogEntry{
						{
							Namespace: namespace1Config.Name,
							Operation: AddNamespace,
							Config:    namespace1Config,
							Timestamp: time.Now(),
						},
					}
					iter := NewMockChangelogIterator(changelog)

					return iter, nil
				})

				store.EXPECT().TransactRelationTuples(gomock.Any(),
					[]*InternalRelationTuple{
						{
							Namespace: namespace1Config.Name,
							Object:    "object1",
							Relation:  "relation1",
							Subject:   &SubjectID{ID: "subject1"},
						},
					},
					[]*InternalRelationTuple{
						{
							Namespace: namespace1Config.Name,
							Object:    "object2",
							Relation:  "relation1",
							Subject:   &SubjectID{ID: "subject2"},
						},
					}).Return(nil)
			},
		},
		{
			name: "Test-2",
			input: &aclpb.WriteRelationTuplesTxnRequest{
				RelationTupleDeltas: []*aclpb.RelationTupleDelta{
					{
						Action: aclpb.RelationTupleDelta_ACTION_INSERT,
						RelationTuple: &aclpb.RelationTuple{
							Namespace: namespace1Config.Name,
						},
					},
				},
			},
			output: output{
				err: NamespaceConfigError{
					Message: fmt.Sprintf("'%s' namespace is undefined. If you recently added it, it may take a couple minutes to propagate", namespace1Config.Name),
					Type:    NamespaceDoesntExist,
				}.ToStatus().Err(),
			},
			mockController: func(store *MockRelationTupleStore, nsmanager *MockNamespaceManager) {
				nsmanager.EXPECT().TopChanges(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(func(ctx context.Context, n uint) (ChangelogIterator, error) {
					changelog := []*NamespaceChangelogEntry{}
					iter := NewMockChangelogIterator(changelog)
					return iter, nil
				})
			},
		},
		{
			name: "Test-3",
			input: &aclpb.WriteRelationTuplesTxnRequest{
				RelationTupleDeltas: []*aclpb.RelationTupleDelta{
					{
						Action: aclpb.RelationTupleDelta_ACTION_INSERT,
						RelationTuple: &aclpb.RelationTuple{
							Namespace: namespace1Config.Name,
							Object:    "object1",
							Relation:  "relation2", // undefined relation for the namespace
							Subject: &aclpb.Subject{
								Ref: &aclpb.Subject_Id{Id: "subject1"},
							},
						},
					},
				},
			},
			output: output{
				err: NamespaceConfigError{
					Message: fmt.Sprintf("'%s' relation is undefined in namespace '%s' at snapshot config timestamp '%s'", "relation2", namespace1Config.Name, timestamp1.Round(0).UTC()),
					Type:    NamespaceRelationUndefined,
				}.ToStatus().Err(),
			},
			mockController: func(store *MockRelationTupleStore, nsmanager *MockNamespaceManager) {
				nsmanager.EXPECT().TopChanges(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(func(ctx context.Context, n uint) (ChangelogIterator, error) {
					changelog := []*NamespaceChangelogEntry{
						{
							Namespace: namespace1Config.Name,
							Operation: AddNamespace,
							Config:    namespace1Config,
							Timestamp: timestamp1,
						},
					}
					iter := NewMockChangelogIterator(changelog)
					return iter, nil
				})
			},
		},
		{
			name: "Test-4",
			input: &aclpb.WriteRelationTuplesTxnRequest{
				RelationTupleDeltas: []*aclpb.RelationTupleDelta{
					{
						Action: aclpb.RelationTupleDelta_ACTION_INSERT,
						RelationTuple: &aclpb.RelationTuple{
							Namespace: namespace1Config.Name,
							Object:    "object1",
							Relation:  "relation1",
							Subject: &aclpb.Subject{
								Ref: subjectSet.ToProto().GetRef(), // SubjectSet references an undefined namespace
							},
						},
					},
				},
			},
			output: output{
				err: NamespaceConfigError{
					Message: fmt.Sprintf("SubjectSet '%s' references the '%s' namespace which is undefined. If this namespace was recently added, please try again in a couple minutes", subjectSet, subjectSet.Namespace),
					Type:    NamespaceDoesntExist,
				}.ToStatus().Err(),
			},
			mockController: func(store *MockRelationTupleStore, nsmanager *MockNamespaceManager) {
				nsmanager.EXPECT().TopChanges(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(func(ctx context.Context, n uint) (ChangelogIterator, error) {
					changelog := []*NamespaceChangelogEntry{
						{
							Namespace: namespace1Config.Name,
							Operation: AddNamespace,
							Config:    namespace1Config,
							Timestamp: timestamp1,
						},
					}
					iter := NewMockChangelogIterator(changelog)
					return iter, nil
				})
			},
		},
		{
			name: "Test-5",
			input: &aclpb.WriteRelationTuplesTxnRequest{
				RelationTupleDeltas: []*aclpb.RelationTupleDelta{
					{
						Action: aclpb.RelationTupleDelta_ACTION_INSERT,
						RelationTuple: &aclpb.RelationTuple{
							Namespace: namespace1Config.Name,
							Object:    "object1",
							Relation:  "relation1",
							Subject: &aclpb.Subject{
								Ref: subjectSet.ToProto().GetRef(),
							},
						},
					},
				},
			},
			output: output{
				err: NamespaceConfigError{
					Message: fmt.Sprintf("SubjectSet '%s' references relation '%s' which is undefined in the namespace '%s' at snapshot config timestamp '%s'. If this relation was recently added to the config, please try again in a couple minutes", subjectSet, "relation2", subjectSet.Namespace, timestamp1.Round(0).UTC()),
					Type:    NamespaceRelationUndefined,
				}.ToStatus().Err(),
			},
			mockController: func(store *MockRelationTupleStore, nsmanager *MockNamespaceManager) {
				nsmanager.EXPECT().TopChanges(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(func(ctx context.Context, n uint) (ChangelogIterator, error) {
					changelog := []*NamespaceChangelogEntry{
						{
							Namespace: namespace1Config.Name,
							Operation: AddNamespace,
							Config:    namespace1Config,
							Timestamp: timestamp1,
						},
						{
							Namespace: namespace2Config.Name,
							Operation: AddNamespace,
							Config:    namespace2Config,
							Timestamp: timestamp1,
						},
					}
					iter := NewMockChangelogIterator(changelog)
					return iter, nil
				})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {

			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockStore := NewMockRelationTupleStore(ctrl)
			mockNamespaceManager := NewMockNamespaceManager(ctrl)

			if test.mockController != nil {
				test.mockController(mockStore, mockNamespaceManager)
			}

			opts := []AccessControllerOption{
				WithStore(mockStore),
				WithNamespaceManager(mockNamespaceManager),
			}

			controller, err := NewAccessController(opts...)
			if err != nil {
				t.Fatalf("Failed to initialize the AccessController: %v", err)
			}
			defer func() {
				if err := controller.Close(); err != nil {
					t.Fatalf("Failed to close the controller: %v", err)
				}
			}()

			response, err := controller.WriteRelationTuplesTxn(context.Background(), test.input)

			if !errors.Is(err, test.output.err) {
				t.Errorf("Expected error '%v', but got '%v'", err, test.output.err)
			}

			if !proto.Equal(response, test.output.response) {
				t.Errorf("Expected response '%v', but got '%v'", response, test.output.response)
			}
		})
	}
}

func TestAccessController_ListRelationTuples(t *testing.T) {

	var dbError = errors.New("some error")

	subID := &SubjectID{ID: "subject1"}
	subjectSet := &SubjectSet{
		Namespace: "namespace2",
		Object:    "object2",
		Relation:  "relation2",
	}

	type output struct {
		response *aclpb.ListRelationTuplesResponse
		err      error
	}

	tests := []struct {
		name  string
		input *aclpb.ListRelationTuplesRequest
		output
		mockController func(store *MockRelationTupleStore, nsmanager *MockNamespaceManager)
	}{
		{
			name:  "Store Error",
			input: &aclpb.ListRelationTuplesRequest{},
			output: output{
				err: dbError,
			},
			mockController: func(store *MockRelationTupleStore, nsmanager *MockNamespaceManager) {
				nsmanager.EXPECT().TopChanges(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(func(ctx context.Context, n uint) (ChangelogIterator, error) {
					changelog := []*NamespaceChangelogEntry{}
					iter := NewMockChangelogIterator(changelog)
					return iter, nil
				})

				store.EXPECT().ListRelationTuples(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, dbError)
			},
		},
		{
			name:  "Successful Response",
			input: &aclpb.ListRelationTuplesRequest{},
			output: output{
				response: &aclpb.ListRelationTuplesResponse{
					RelationTuples: []*aclpb.RelationTuple{
						{
							Namespace: "namespace1",
							Object:    "object1",
							Relation:  "relation1",
							Subject:   subID.ToProto(),
						},
						{
							Namespace: "namespace1",
							Object:    "object1",
							Relation:  "relation1",
							Subject:   subjectSet.ToProto(),
						},
					},
				},
			},
			mockController: func(store *MockRelationTupleStore, nsmanager *MockNamespaceManager) {
				nsmanager.EXPECT().TopChanges(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(func(ctx context.Context, n uint) (ChangelogIterator, error) {
					changelog := []*NamespaceChangelogEntry{}
					iter := NewMockChangelogIterator(changelog)
					return iter, nil
				})

				store.EXPECT().ListRelationTuples(gomock.Any(), gomock.Any(), gomock.Any()).Return(
					[]InternalRelationTuple{
						{
							Namespace: "namespace1",
							Object:    "object1",
							Relation:  "relation1",
							Subject:   subID,
						},
						{
							Namespace: "namespace1",
							Object:    "object1",
							Relation:  "relation1",
							Subject:   subjectSet,
						},
					},
					nil,
				)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {

			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockStore := NewMockRelationTupleStore(ctrl)
			mockNamespaceManager := NewMockNamespaceManager(ctrl)

			if test.mockController != nil {
				test.mockController(mockStore, mockNamespaceManager)
			}

			opts := []AccessControllerOption{
				WithStore(mockStore),
				WithNamespaceManager(mockNamespaceManager),
			}

			controller, err := NewAccessController(opts...)
			if err != nil {
				t.Fatalf("Failed to initialize the AccessController: %v", err)
			}
			defer func() {
				if err := controller.Close(); err != nil {
					t.Fatalf("Failed to close the controller: %v", err)
				}
			}()

			response, err := controller.ListRelationTuples(context.Background(), test.input)

			if !errors.Is(err, test.output.err) {
				t.Errorf("Expected error '%v', but got '%v'", err, test.output.err)
			}

			if !proto.Equal(response, test.output.response) {
				t.Errorf("Expected response '%v', but got '%v'", response, test.output.response)
			}
		})
	}
}

func TestAccessController_AddConfig(t *testing.T) {

	type output struct {
		response *aclpb.AddConfigResponse
		err      error
	}

	dbError := errors.New("db error")

	config1 := &aclpb.NamespaceConfig{
		Name: "namespace1",
		Relations: []*aclpb.Relation{
			{Name: "relation1"},
		},
	}

	validRequest := &aclpb.AddConfigRequest{
		Config: config1,
	}

	tests := []struct {
		name  string
		input *aclpb.AddConfigRequest
		output
		mockController func(nsmanager *MockNamespaceManager)
	}{
		{
			name:  "Test-1: Database Error",
			input: validRequest,
			output: output{
				err: dbError,
			},
			mockController: func(nsmanager *MockNamespaceManager) {
				nsmanager.EXPECT().AddConfig(gomock.Any(), validRequest.Config).Return(dbError)
			},
		},
		{
			name:  "Test-2: Successful Get",
			input: validRequest,
			output: output{
				response: &aclpb.AddConfigResponse{},
			},
			mockController: func(nsmanager *MockNamespaceManager) {
				nsmanager.EXPECT().AddConfig(gomock.Any(), validRequest.Config).Return(nil)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {

			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockNamespaceManager := NewMockNamespaceManager(ctrl)

			mockNamespaceManager.EXPECT().TopChanges(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(func(ctx context.Context, n uint) (ChangelogIterator, error) {
				changelog := []*NamespaceChangelogEntry{}
				iter := NewMockChangelogIterator(changelog)
				return iter, nil
			})

			if test.mockController != nil {
				test.mockController(mockNamespaceManager)
			}

			opts := []AccessControllerOption{
				WithNamespaceManager(mockNamespaceManager),
			}

			controller, err := NewAccessController(opts...)
			if err != nil {
				t.Fatalf("Failed to initialize the AccessController: %v", err)
			}
			defer func() {
				if err := controller.Close(); err != nil {
					t.Fatalf("Failed to close the controller: %v", err)
				}
			}()

			response, err := controller.AddConfig(context.Background(), test.input)

			if !errors.Is(err, test.output.err) {
				t.Errorf("Expected error '%v', but got '%v'", err, test.output.err)
			}

			if !proto.Equal(response, test.output.response) {
				t.Errorf("Expected response '%v', but got '%v'", response, test.output.response)
			}
		})
	}
}

func TestAccessController_ReadConfig(t *testing.T) {

	type output struct {
		response *aclpb.ReadConfigResponse
		err      error
	}

	dbError := errors.New("db error")

	validRequest := &aclpb.ReadConfigRequest{
		Namespace: "namespace1",
	}

	config1 := &aclpb.NamespaceConfig{
		Name: "namespace1",
		Relations: []*aclpb.Relation{
			{Name: "relation1"},
		},
	}

	tests := []struct {
		name  string
		input *aclpb.ReadConfigRequest
		output
		mockController func(nsmanager *MockNamespaceManager)
	}{
		{
			name:  "Test-1: Database Error",
			input: validRequest,
			output: output{
				err: dbError,
			},
			mockController: func(nsmanager *MockNamespaceManager) {
				nsmanager.EXPECT().GetConfig(gomock.Any(), validRequest.Namespace).Return(nil, dbError)
			},
		},
		{
			name:  "Test-2: Successful Get",
			input: validRequest,
			output: output{
				response: &aclpb.ReadConfigResponse{
					Namespace: validRequest.Namespace,
					Config:    config1,
				},
			},
			mockController: func(nsmanager *MockNamespaceManager) {
				nsmanager.EXPECT().GetConfig(gomock.Any(), validRequest.Namespace).Return(config1, nil)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {

			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockNamespaceManager := NewMockNamespaceManager(ctrl)

			mockNamespaceManager.EXPECT().TopChanges(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(func(ctx context.Context, n uint) (ChangelogIterator, error) {
				changelog := []*NamespaceChangelogEntry{}
				iter := NewMockChangelogIterator(changelog)
				return iter, nil
			})

			if test.mockController != nil {
				test.mockController(mockNamespaceManager)
			}

			opts := []AccessControllerOption{
				WithNamespaceManager(mockNamespaceManager),
			}

			controller, err := NewAccessController(opts...)
			if err != nil {
				t.Fatalf("Failed to initialize the AccessController: %v", err)
			}
			defer func() {
				if err := controller.Close(); err != nil {
					t.Fatalf("Failed to close the controller: %v", err)
				}
			}()

			response, err := controller.ReadConfig(context.Background(), test.input)

			if !errors.Is(err, test.output.err) {
				t.Errorf("Expected error '%v', but got '%v'", err, test.output.err)
			}

			if !proto.Equal(response, test.output.response) {
				t.Errorf("Expected response '%v', but got '%v'", response, test.output.response)
			}
		})
	}
}

func TestAccessController_WriteRelation(t *testing.T) {

	type output struct {
		response *aclpb.WriteRelationResponse
		err      error
	}

	dbError := errors.New("db error")

	validRequest := &aclpb.WriteRelationRequest{
		Namespace: "namespace1",
		Relation: &aclpb.Relation{
			Name: "relation1",
		},
	}

	tests := []struct {
		name  string
		input *aclpb.WriteRelationRequest
		output
		mockController func(nsmanager *MockNamespaceManager)
	}{
		{
			name:  "Test-1: Database Upsert Error",
			input: validRequest,
			output: output{
				err: dbError,
			},
			mockController: func(nsmanager *MockNamespaceManager) {
				nsmanager.EXPECT().UpsertRelation(gomock.Any(), validRequest.Namespace, validRequest.Relation).Return(dbError)
			},
		},
		{
			name:  "Test-2: Successful Upsert",
			input: validRequest,
			output: output{
				response: &aclpb.WriteRelationResponse{},
			},
			mockController: func(nsmanager *MockNamespaceManager) {
				nsmanager.EXPECT().UpsertRelation(gomock.Any(), validRequest.Namespace, validRequest.Relation).Return(nil)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {

			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockNamespaceManager := NewMockNamespaceManager(ctrl)

			mockNamespaceManager.EXPECT().TopChanges(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(func(ctx context.Context, n uint) (ChangelogIterator, error) {
				changelog := []*NamespaceChangelogEntry{}
				iter := NewMockChangelogIterator(changelog)
				return iter, nil
			})

			if test.mockController != nil {
				test.mockController(mockNamespaceManager)
			}

			opts := []AccessControllerOption{
				WithNamespaceManager(mockNamespaceManager),
			}

			controller, err := NewAccessController(opts...)
			if err != nil {
				t.Fatalf("Failed to initialize the AccessController: %v", err)
			}
			defer func() {
				if err := controller.Close(); err != nil {
					t.Fatalf("Failed to close the controller: %v", err)
				}
			}()

			response, err := controller.WriteRelation(context.Background(), test.input)

			if !errors.Is(err, test.output.err) {
				t.Errorf("Expected error '%v', but got '%v'", err, test.output.err)
			}

			if !proto.Equal(response, test.output.response) {
				t.Errorf("Expected response '%v', but got '%v'", response, test.output.response)
			}
		})
	}
}

// func TestAccessController_LocalStatePanic(t *testing.T) {
// 	defer func() {
// 		if r := recover(); r == nil {
// 			t.Errorf("Expected the code to panic, but it didn't")
// 		}
// 	}()

// 	ctrl := gomock.NewController(t)
// 	defer ctrl.Finish()

// 	mockNamespaceManager := NewMockNamespaceManager(ctrl)

// 	mockNamespaceManager.EXPECT().TopChanges(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(func(ctx context.Context, n uint) (ChangelogIterator, error) {
// 		changelog := []*NamespaceChangelogEntry{}
// 		iter := NewMockChangelogIterator(changelog)
// 		return iter, nil
// 	})

// 	opts := []AccessControllerOption{
// 		WithNamespaceManager(mockNamespaceManager),
// 	}

// 	controller, err := NewAccessController(opts...)
// 	if err != nil {
// 		t.Fatalf("Failed to initialize the AccessController: %v", err)
// 	}
// 	defer func() {
// 		if err := controller.Close(); err != nil {
// 			t.Fatalf("Failed to close the controller: %v", err)
// 		}
// 	}()

// 	controller.LocalState(false)
// }

func TestAccessController_LocalState(t *testing.T) {

	timestamp1 := time.Now()

	config1 := &aclpb.NamespaceConfig{
		Name: "namespace1",
		Relations: []*aclpb.Relation{
			{Name: "relation1"},
		},
	}

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockNamespaceManager := NewMockNamespaceManager(ctrl)

	mockNamespaceManager.EXPECT().TopChanges(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(func(ctx context.Context, n uint) (ChangelogIterator, error) {
		changelog := []*NamespaceChangelogEntry{
			{
				Namespace: config1.Name,
				Operation: AddNamespace,
				Config:    config1,
				Timestamp: timestamp1,
			},
		}
		iter := NewMockChangelogIterator(changelog)
		return iter, nil
	})

	opts := []AccessControllerOption{
		WithNamespaceManager(mockNamespaceManager),
		WithNodeConfigs(NodeConfigs{
			ServerID:   "server1",
			ServerPort: 50052,
		}),
	}

	controller, err := NewAccessController(opts...)
	if err != nil {
		t.Fatalf("Failed to initialize the AccessController: %v", err)
	}
	defer func() {
		if err := controller.Close(); err != nil {
			t.Fatalf("Failed to close the controller: %v", err)
		}
	}()

	jsonConfig1, err := protojson.Marshal(config1)
	if err != nil {
		t.Fatalf("Failed to protojson.Marshal the namespace config: %v", err)
	}

	snapshots := map[string]map[time.Time][]byte{
		config1.Name: {
			timestamp1.Round(0).UTC(): jsonConfig1,
		},
	}

	expected, err := json.Marshal(NodeMetadata{
		NodeID:                   "server1",
		ServerPort:               50052,
		NamespaceConfigSnapshots: snapshots,
	})
	if err != nil {
		t.Fatalf("Failed to json.Marshal the NodeMetadata: %v", err)
	}

	state := controller.LocalState(false)

	if !reflect.DeepEqual(expected, state) {
		t.Errorf("Expected state '%s', but got '%s'", expected, state)
	}
}

func TestAccessController_MergeRemoteState(t *testing.T) {

	type input struct {
		buf  []byte
		join bool
	}

	timestamp1 := time.Now().Round(0).UTC()

	config1 := &aclpb.NamespaceConfig{
		Name: "namespace1",
		Relations: []*aclpb.Relation{
			{Name: "relation1"},
		},
	}

	jsonConfig1, err := protojson.Marshal(config1)
	if err != nil {
		t.Fatalf("Failed to protojson.Marshal the namespace config: %v", err)
	}

	snapshots := map[string]map[time.Time][]byte{
		config1.Name: {
			timestamp1: jsonConfig1,
		},
	}

	metadata := NodeMetadata{
		NodeID:                   "server1",
		ServerPort:               50052,
		NamespaceConfigSnapshots: snapshots,
	}

	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("Failed to json.Marshal the node metadata: %v", err)
	}

	tests := []struct {
		name string
		input
		mockController func(mpeerStore *MockPeerNamespaceConfigStore)
	}{
		{
			name: "Test-1: Bad Buffer",
			input: input{
				buf: nil,
			},
		},
		{
			name: "Test-2: Set Namespace Configs for Peer",
			input: input{
				buf: metadataBytes,
			},
			mockController: func(mpeerStore *MockPeerNamespaceConfigStore) {
				mpeerStore.EXPECT().SetNamespaceConfigSnapshot(metadata.NodeID, config1.Name, config1, timestamp1)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {

			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockPeerStore := NewMockPeerNamespaceConfigStore(ctrl)

			if test.mockController != nil {
				test.mockController(mockPeerStore)
			}

			controller := &AccessController{
				PeerNamespaceConfigStore: mockPeerStore,
			}

			controller.MergeRemoteState(test.input.buf, test.input.join)
		})
	}
}

func TestAccessController_NofifyJoin(t *testing.T) {

	meta := NodeMetadata{
		NodeID:     "node1",
		ServerPort: 50052,
	}

	metaJSON, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("Failed to json.Marshal the node metadata: %v", err)
	}

	member1 := &memberlist.Node{Name: "node1"}
	member2 := &memberlist.Node{Name: "node2", Meta: metaJSON}
	member3 := &memberlist.Node{Name: "node3", Meta: []byte("badjson")}

	tests := []struct {
		name       string
		member     *memberlist.Node
		mockExpect func(mrouter *MockClientRouter, mring *MockHashring)
	}{
		{
			name:   "Test-1",
			member: member1,
			mockExpect: func(mrouter *MockClientRouter, mring *MockHashring) {
				gomock.InOrder(
					mring.EXPECT().Add(member1),
					mring.EXPECT().Checksum(),
				)
			},
		},
		{
			name:   "Test-2",
			member: member2,
			mockExpect: func(mrouter *MockClientRouter, mring *MockHashring) {

				var rpcClient aclpb.CheckServiceClient = aclpb.NewCheckServiceClient(nil)
				gomock.InOrder(
					mrouter.EXPECT().AddClient("node2", gomock.AssignableToTypeOf(rpcClient)),
					mring.EXPECT().Add(member2),
					mring.EXPECT().Checksum(),
				)
			},
		},
		{
			name:   "Test-3",
			member: member3,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockRPCRouter := NewMockClientRouter(ctrl)
			mockHashring := NewMockHashring(ctrl)

			if test.mockExpect != nil {
				test.mockExpect(mockRPCRouter, mockHashring)
			}

			controller := &AccessController{
				NodeConfigs: NodeConfigs{
					ServerID: "node1",
				},
				RPCRouter: mockRPCRouter,
				Hashring:  mockHashring,
			}

			controller.NotifyJoin(test.member)
		})
	}
}

func TestAccessController_NotifyLeave(t *testing.T) {

	member1 := &memberlist.Node{Name: "node1"}
	member2 := &memberlist.Node{Name: "node2"}

	tests := []struct {
		name       string
		member     *memberlist.Node
		mockExpect func(mrouter *MockClientRouter, mring *MockHashring, mpeerStore *MockPeerNamespaceConfigStore)
	}{
		{
			name:   "Test-1",
			member: member1,
			mockExpect: func(mrouter *MockClientRouter, mring *MockHashring, mpeerStore *MockPeerNamespaceConfigStore) {
				gomock.InOrder(
					mring.EXPECT().Remove(member1),
					mring.EXPECT().Checksum(),
					mpeerStore.EXPECT().DeleteNamespaceConfigSnapshots(member1.Name),
				)
			},
		},
		{
			name:   "Test-2",
			member: member2,
			mockExpect: func(mrouter *MockClientRouter, mring *MockHashring, mpeerStore *MockPeerNamespaceConfigStore) {
				gomock.InOrder(
					mrouter.EXPECT().RemoveClient("node2"),
					mring.EXPECT().Remove(member2),
					mring.EXPECT().Checksum(),
					mpeerStore.EXPECT().DeleteNamespaceConfigSnapshots(member2.Name),
				)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockRPCRouter := NewMockClientRouter(ctrl)
			mockHashring := NewMockHashring(ctrl)
			mockPeerStore := NewMockPeerNamespaceConfigStore(ctrl)

			if test.mockExpect != nil {
				test.mockExpect(mockRPCRouter, mockHashring, mockPeerStore)
			}

			controller := &AccessController{
				NodeConfigs: NodeConfigs{
					ServerID: member1.Name,
				},
				PeerNamespaceConfigStore: mockPeerStore,
				RPCRouter:                mockRPCRouter,
				Hashring:                 mockHashring,
			}

			controller.NotifyLeave(test.member)
		})
	}
}

func TestAccessController_rewriteFromNamespaceConfig(t *testing.T) {

	type input struct {
		relation string
		config   *aclpb.NamespaceConfig
	}

	tests := []struct {
		name string
		input
		output *aclpb.Rewrite
	}{
		{
			name: "Test-1",
			input: input{
				relation: "relation1",
				config: &aclpb.NamespaceConfig{
					Name: "namespace1",
				},
			},
		},
		{
			name: "Test-2",
			input: input{
				relation: "relation1",
				config: &aclpb.NamespaceConfig{
					Name: "namespace1",
					Relations: []*aclpb.Relation{
						{Name: "relation2"},
					},
				},
			},
		},
		{
			name: "Test-3",
			input: input{
				relation: "relation1",
				config: &aclpb.NamespaceConfig{
					Name: "namespace1",
					Relations: []*aclpb.Relation{
						{Name: "relation1"},
					},
				},
			},
			output: &aclpb.Rewrite{
				RewriteOperation: &aclpb.Rewrite_Union{
					Union: &aclpb.SetOperation{
						Children: []*aclpb.SetOperation_Child{
							{ChildType: &aclpb.SetOperation_Child_This_{}},
						},
					},
				},
			},
		},
		{
			name: "Test-4",
			input: input{
				relation: "relation1",
				config: &aclpb.NamespaceConfig{
					Name: "namespace1",
					Relations: []*aclpb.Relation{
						{
							Name: "relation1",
							Rewrite: &aclpb.Rewrite{
								RewriteOperation: &aclpb.Rewrite_Union{
									Union: &aclpb.SetOperation{
										Children: []*aclpb.SetOperation_Child{
											{
												ChildType: &aclpb.SetOperation_Child_ComputedSubjectset{
													ComputedSubjectset: &aclpb.ComputedSubjectset{Relation: "relation2"},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			output: &aclpb.Rewrite{
				RewriteOperation: &aclpb.Rewrite_Union{
					Union: &aclpb.SetOperation{
						Children: []*aclpb.SetOperation_Child{
							{
								ChildType: &aclpb.SetOperation_Child_ComputedSubjectset{
									ComputedSubjectset: &aclpb.ComputedSubjectset{Relation: "relation2"},
								},
							},
						},
					},
				},
			},
		},
	}

	for _, test := range tests {

		rewrite := rewriteFromNamespaceConfig(test.input.relation, test.input.config)

		if !proto.Equal(rewrite, test.output) {
			t.Errorf("Expected '%v', but got '%v'", test.output, rewrite)
		}
	}
}
