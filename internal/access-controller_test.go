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
	"github.com/authorizer-tech/access-controller/internal/hashring"
	namespacemgr "github.com/authorizer-tech/access-controller/internal/namespace-manager"
	"github.com/golang/mock/gomock"
	"github.com/hashicorp/memberlist"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

var dirsConfig = &aclpb.NamespaceConfig{
	Name: "dirs",
	Relations: []*aclpb.Relation{
		{Name: "viewer"},
	},
}

var filesConfig = &aclpb.NamespaceConfig{
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

func TestAccessController_Expand(t *testing.T) {

	timestamp := time.Now()

	type output struct {
		response *aclpb.ExpandResponse
		err      error
	}

	tests := []struct {
		name  string
		input *aclpb.ExpandRequest
		output
		mockController func(store *MockRelationTupleStore)
	}{
		{
			name: "Test-1: Undefined Namespace",
			input: &aclpb.ExpandRequest{
				SubjectSet: &aclpb.SubjectSet{
					Namespace: "undefined",
					Object:    "object1",
					Relation:  "relation1",
				},
			},
			output: output{
				err: namespacemgr.NamespaceConfigError{
					Message: fmt.Sprintf("'%s' namespace is undefined. If you recently added it, it may take a couple minutes to propagate", "undefined"),
					Type:    namespacemgr.NamespaceDoesntExist,
				}.ToStatus().Err(),
			},
		},
		{
			name: "Test-2: Expand without any rewrites",
			input: &aclpb.ExpandRequest{
				SubjectSet: &aclpb.SubjectSet{
					Namespace: "files",
					Object:    "file1",
					Relation:  "owner",
				},
			},
			output: output{
				response: &aclpb.ExpandResponse{
					Tree: &aclpb.SubjectTree{
						NodeType: aclpb.NodeType_NODE_TYPE_UNION,
						Subject: &aclpb.Subject{
							Ref: &aclpb.Subject_Set{
								Set: &aclpb.SubjectSet{
									Namespace: "files",
									Object:    "file1",
									Relation:  "owner",
								},
							},
						},
						Children: []*aclpb.SubjectTree{
							{
								NodeType: aclpb.NodeType_NODE_TYPE_LEAF,
								Subject: &aclpb.Subject{
									Ref: &aclpb.Subject_Id{Id: "subject1"},
								},
							},
						},
					},
				},
			},
			mockController: func(store *MockRelationTupleStore) {
				store.EXPECT().ListRelationTuples(gomock.Any(), &aclpb.ListRelationTuplesRequest_Query{
					Namespace: "files",
					Object:    "file1",
					Relations: []string{"owner"},
				}).Return([]InternalRelationTuple{
					{
						Namespace: "files",
						Object:    "file1",
						Relation:  "owner",
						Subject:   &SubjectID{ID: "subject1"},
					},
				}, nil)
			},
		},
		{
			name: "Test-3: Expand without rewrites but with SubjectSet indirection",
			input: &aclpb.ExpandRequest{
				SubjectSet: &aclpb.SubjectSet{
					Namespace: "files",
					Object:    "file1",
					Relation:  "viewer",
				},
			},
			output: output{
				response: &aclpb.ExpandResponse{
					Tree: &aclpb.SubjectTree{
						NodeType: aclpb.NodeType_NODE_TYPE_UNION,
						Subject: &aclpb.Subject{
							Ref: &aclpb.Subject_Set{
								Set: &aclpb.SubjectSet{
									Namespace: "files",
									Object:    "file1",
									Relation:  "viewer",
								},
							},
						},
						Children: []*aclpb.SubjectTree{
							{
								NodeType: aclpb.NodeType_NODE_TYPE_UNION,
								Subject: &aclpb.Subject{
									Ref: &aclpb.Subject_Set{
										Set: &aclpb.SubjectSet{
											Namespace: "dirs",
											Object:    "dir1",
											Relation:  "viewer",
										},
									},
								},
								Children: []*aclpb.SubjectTree{
									{
										NodeType: aclpb.NodeType_NODE_TYPE_LEAF,
										Subject: &aclpb.Subject{
											Ref: &aclpb.Subject_Id{Id: "subject1"},
										},
									},
								},
							},
						},
					},
				},
			},
			mockController: func(store *MockRelationTupleStore) {

				store.EXPECT().ListRelationTuples(gomock.Any(), &aclpb.ListRelationTuplesRequest_Query{
					Namespace: "files",
					Object:    "file1",
					Relations: []string{"parent"},
				}).Return([]InternalRelationTuple{
					{
						Namespace: "files",
						Object:    "file1",
						Relation:  "parent",
						Subject: &SubjectSet{
							Namespace: "dirs",
							Object:    "dir1",
							Relation:  "...",
						},
					},
				}, nil)

				store.EXPECT().ListRelationTuples(gomock.Any(), &aclpb.ListRelationTuplesRequest_Query{
					Namespace: "dirs",
					Object:    "dir1",
					Relations: []string{"viewer"},
				}).Return([]InternalRelationTuple{
					{
						Namespace: "dirs",
						Object:    "dir1",
						Relation:  "viewer",
						Subject:   &SubjectID{ID: "subject1"},
					},
				}, nil)
			},
		},
		{
			name: "Test-4: Expand with nested rewrites",
			input: &aclpb.ExpandRequest{
				SubjectSet: &aclpb.SubjectSet{
					Namespace: "files",
					Object:    "file1",
					Relation:  "editor",
				},
			},
			output: output{
				response: &aclpb.ExpandResponse{
					Tree: &aclpb.SubjectTree{
						NodeType: aclpb.NodeType_NODE_TYPE_INTERSECTION,
						Subject: &aclpb.Subject{Ref: &aclpb.Subject_Set{
							Set: &aclpb.SubjectSet{
								Namespace: "files",
								Object:    "file1",
								Relation:  "editor",
							},
						}},
						Children: []*aclpb.SubjectTree{
							{
								NodeType: aclpb.NodeType_NODE_TYPE_UNION,
								Subject: &aclpb.Subject{Ref: &aclpb.Subject_Set{
									Set: &aclpb.SubjectSet{
										Namespace: "files",
										Object:    "file1",
										Relation:  "editor",
									},
								}},
								Children: []*aclpb.SubjectTree{
									{
										NodeType: aclpb.NodeType_NODE_TYPE_LEAF,
										Subject:  &aclpb.Subject{Ref: &aclpb.Subject_Id{Id: "subject1"}},
									},
									{
										NodeType: aclpb.NodeType_NODE_TYPE_UNION,
										Subject: &aclpb.Subject{Ref: &aclpb.Subject_Set{
											Set: &aclpb.SubjectSet{
												Namespace: "files",
												Object:    "file1",
												Relation:  "owner",
											},
										}},
										Children: []*aclpb.SubjectTree{
											{
												NodeType: aclpb.NodeType_NODE_TYPE_LEAF,
												Subject:  &aclpb.Subject{Ref: &aclpb.Subject_Id{Id: "subject2"}},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			mockController: func(store *MockRelationTupleStore) {
				store.EXPECT().ListRelationTuples(gomock.Any(), &aclpb.ListRelationTuplesRequest_Query{
					Namespace: "files",
					Object:    "file1",
					Relations: []string{"editor"},
				}).Return([]InternalRelationTuple{
					{
						Namespace: "files",
						Object:    "file1",
						Relation:  "editor",
						Subject:   &SubjectID{ID: "subject1"},
					},
				}, nil)

				store.EXPECT().ListRelationTuples(gomock.Any(), &aclpb.ListRelationTuplesRequest_Query{
					Namespace: "files",
					Object:    "file1",
					Relations: []string{"owner"},
				}).Return([]InternalRelationTuple{
					{
						Namespace: "files",
						Object:    "file1",
						Relation:  "owner",
						Subject:   &SubjectID{ID: "subject2"},
					},
				}, nil)
			},
		},
		{
			name:  "Test-5: ExpandRequest.SubjectSet undefined",
			input: &aclpb.ExpandRequest{},
			output: output{
				err: status.Error(codes.InvalidArgument, "'subjectSet' is a required field and cannot be nil"),
			},
		},
		{
			name: "Test-6: ExpandRequest.SubjectSet.Namespace undefined",
			input: &aclpb.ExpandRequest{
				SubjectSet: &aclpb.SubjectSet{},
			},
			output: output{
				err: status.Error(codes.InvalidArgument, "'subjectSet.namespace' is a required field and cannot be empty"),
			},
		},
		{
			name: "Test-7: ExpandRequest.SubjectSet.Object undefined",
			input: &aclpb.ExpandRequest{
				SubjectSet: &aclpb.SubjectSet{
					Namespace: "namespace1",
				},
			},
			output: output{
				err: status.Error(codes.InvalidArgument, "'subjectSet.object' is a required field and cannot be empty"),
			},
		},
		{
			name: "Test-8: ExpandRequest.SubjectSet.Relation undefined",
			input: &aclpb.ExpandRequest{
				SubjectSet: &aclpb.SubjectSet{
					Namespace: "namespace1",
					Object:    "object1",
				},
			},
			output: output{
				err: status.Error(codes.InvalidArgument, "'subjectSet.relation' is a required field and cannot be empty"),
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

			mockNamespaceManager.EXPECT().TopChanges(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(func(ctx context.Context, n uint) (namespacemgr.ChangelogIterator, error) {
				changelog := []*namespacemgr.NamespaceChangelogEntry{
					{
						Namespace: filesConfig.Name,
						Operation: namespacemgr.AddNamespace,
						Config:    filesConfig,
						Timestamp: timestamp,
					},
					{
						Namespace: dirsConfig.Name,
						Operation: namespacemgr.AddNamespace,
						Config:    dirsConfig,
						Timestamp: timestamp,
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

			response, err := controller.Expand(context.Background(), test.input)

			if !errors.Is(err, test.output.err) {
				t.Errorf("Expected error '%v', but got '%v'", err, test.output.err)
			}

			if !proto.Equal(response, test.output.response) {
				t.Errorf("Expected '%v', but got '%v'", test.output.response, response)
			}
		})
	}
}

func TestAccessController_Check(t *testing.T) {

	dbError := errors.New("database error")

	timestamp1 := time.Now()

	groupsConfig := &aclpb.NamespaceConfig{
		Name: "groups",
		Relations: []*aclpb.Relation{
			{Name: "member"},
		},
	}

	type input struct {
		ctx context.Context
		req *aclpb.CheckRequest
	}

	type output struct {
		response *aclpb.CheckResponse
		err      error
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
				ctx: context.Background(),
				req: &aclpb.CheckRequest{
					Namespace: "groups",
					Object:    "group1",
					Relation:  "member",
					Subject:   &aclpb.Subject{Ref: &aclpb.Subject_Id{Id: "subject1"}},
				},
			},
			output: output{
				response: &aclpb.CheckResponse{
					Allowed: true,
				},
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
				ctx: context.Background(),
				req: &aclpb.CheckRequest{
					Namespace: "groups",
					Object:    "group1",
					Relation:  "member",
					Subject:   &aclpb.Subject{Ref: &aclpb.Subject_Id{Id: "subject1"}},
				},
			},
			output: output{
				response: &aclpb.CheckResponse{
					Allowed: false,
				},
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
				ctx: context.Background(),
				req: &aclpb.CheckRequest{
					Namespace: "groups",
					Object:    "group1",
					Relation:  "member",
					Subject:   &aclpb.Subject{Ref: &aclpb.Subject_Id{Id: "subject1"}},
				},
			},
			output: output{
				err: internalErrorStatus,
			},
			mockController: func(mstore *MockRelationTupleStore) {
				mstore.EXPECT().RowCount(gomock.Any(), gomock.Any()).Return(int64(-1), dbError)
			},
		},
		{
			name: "Test-4: Nested Rewrites with allowed outcome",
			input: input{
				ctx: context.Background(),
				req: &aclpb.CheckRequest{
					Namespace: "files",
					Object:    "file1",
					Relation:  "editor",
					Subject:   &aclpb.Subject{Ref: &aclpb.Subject_Id{Id: "subject1"}},
				},
			},
			output: output{
				response: &aclpb.CheckResponse{
					Allowed: true,
				},
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
				ctx: context.Background(),
				req: &aclpb.CheckRequest{
					Namespace: "groups",
					Object:    "group1",
					Relation:  "member",
					Subject:   &aclpb.Subject{Ref: &aclpb.Subject_Id{Id: "subject1"}},
				},
			},
			output: output{
				response: &aclpb.CheckResponse{
					Allowed: true,
				},
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
				ctx: context.Background(),
				req: &aclpb.CheckRequest{
					Namespace: "files",
					Object:    "file1",
					Relation:  "viewer",
					Subject:   &aclpb.Subject{Ref: &aclpb.Subject_Id{Id: "subject1"}},
				},
			},
			output: output{
				response: &aclpb.CheckResponse{
					Allowed: true,
				},
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
				ctx: context.Background(),
				req: &aclpb.CheckRequest{
					Namespace: "files",
					Object:    "file1",
					Relation:  "viewer",
					Subject:   &aclpb.Subject{Ref: &aclpb.Subject_Id{Id: "subject1"}},
				},
			},
			output: output{
				response: &aclpb.CheckResponse{
					Allowed: false,
				},
			},
			mockController: func(mstore *MockRelationTupleStore) {

				mstore.EXPECT().SubjectSets(gomock.Any(), Object{
					Namespace: "files",
					ID:        "file1",
				}, "parent").Return([]SubjectSet{}, nil)
			},
		},
		{
			name: "Test-8: Checksums don't match",
			input: input{
				ctx: hashring.NewContextWithChecksum(context.Background(), 1),
				req: &aclpb.CheckRequest{
					Namespace: "files",
					Object:    "file1",
					Relation:  "viewer",
					Subject:   &aclpb.Subject{Ref: &aclpb.Subject_Id{Id: "subject1"}},
				},
			},
			output: output{
				err: internalErrorStatus,
			},
		},
		{
			name: "Test-9: Top-level undefined namespace",
			input: input{
				ctx: context.Background(),
				req: &aclpb.CheckRequest{
					Namespace: "undefined",
					Object:    "object1",
					Relation:  "relation1",
					Subject:   &aclpb.Subject{Ref: &aclpb.Subject_Id{Id: "subject1"}},
				},
			},
			output: output{
				err: namespacemgr.NamespaceConfigError{
					Message: fmt.Sprintf("'%s' namespace is undefined. If you recently added it, it may take a couple minutes to propagate", "undefined"),
					Type:    namespacemgr.NamespaceDoesntExist,
				}.ToStatus().Err(),
			},
		},
		{
			name: "Test-10: CheckRequest.Namespace is undefined",
			input: input{
				req: &aclpb.CheckRequest{},
			},
			output: output{
				err: status.Error(codes.InvalidArgument, "'namespace' is a required field and cannot be empty"),
			},
		},
		{
			name: "Test-11: CheckRequest.Object is undefined",
			input: input{
				req: &aclpb.CheckRequest{
					Namespace: groupsConfig.Name,
				},
			},
			output: output{
				err: status.Error(codes.InvalidArgument, "'object' is a required field and cannot be empty"),
			},
		},
		{
			name: "Test-12: CheckRequest.Relation is undefined",
			input: input{
				req: &aclpb.CheckRequest{
					Namespace: groupsConfig.Name,
					Object:    "group1",
				},
			},
			output: output{
				err: status.Error(codes.InvalidArgument, "'relation' is a required field and cannot be empty"),
			},
		},
		{
			name: "Test-13: CheckRequest.Subject is undefined",
			input: input{
				req: &aclpb.CheckRequest{
					Namespace: groupsConfig.Name,
					Object:    "group1",
					Relation:  "member",
				},
			},
			output: output{
				err: status.Error(codes.InvalidArgument, "'subject' is a required field and cannot be nil"),
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

			mockNamespaceManager.EXPECT().TopChanges(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(func(ctx context.Context, n uint) (namespacemgr.ChangelogIterator, error) {
				changelog := []*namespacemgr.NamespaceChangelogEntry{
					{
						Namespace: groupsConfig.Name,
						Operation: namespacemgr.AddNamespace,
						Config:    groupsConfig,
						Timestamp: timestamp1,
					},
					{
						Namespace: filesConfig.Name,
						Operation: namespacemgr.AddNamespace,
						Config:    filesConfig,
						Timestamp: timestamp1,
					},
					{
						Namespace: dirsConfig.Name,
						Operation: namespacemgr.AddNamespace,
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

			response, err := controller.Check(test.input.ctx, test.input.req)

			if !errors.Is(err, test.output.err) {
				t.Errorf("Expected error '%v', but got '%v'", test.output.err, err)
			}

			if !proto.Equal(response, test.output.response) {
				t.Errorf("Expected response '%v', but got '%v'", test.output.response, response)
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
				nsmanager.EXPECT().TopChanges(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(func(ctx context.Context, n uint) (namespacemgr.ChangelogIterator, error) {
					changelog := []*namespacemgr.NamespaceChangelogEntry{
						{
							Namespace: namespace1Config.Name,
							Operation: namespacemgr.AddNamespace,
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
							Object:    "object1",
							Relation:  "relation1",
							Subject: &aclpb.Subject{
								Ref: &aclpb.Subject_Id{Id: "subject1"},
							},
						},
					},
				},
			},
			output: output{
				err: namespacemgr.NamespaceConfigError{
					Message: fmt.Sprintf("'%s' namespace is undefined. If you recently added it, it may take a couple minutes to propagate", namespace1Config.Name),
					Type:    namespacemgr.NamespaceDoesntExist,
				}.ToStatus().Err(),
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
				err: namespacemgr.NamespaceConfigError{
					Message: fmt.Sprintf("'%s' relation is undefined in namespace '%s' at snapshot config timestamp '%s'. If this relation was recently added, please try again in a couple minutes", "relation2", namespace1Config.Name, timestamp1.Round(0).UTC()),
					Type:    namespacemgr.NamespaceRelationUndefined,
				}.ToStatus().Err(),
			},
			mockController: func(store *MockRelationTupleStore, nsmanager *MockNamespaceManager) {
				nsmanager.EXPECT().TopChanges(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(func(ctx context.Context, n uint) (namespacemgr.ChangelogIterator, error) {
					changelog := []*namespacemgr.NamespaceChangelogEntry{
						{
							Namespace: namespace1Config.Name,
							Operation: namespacemgr.AddNamespace,
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
				err: namespacemgr.NamespaceConfigError{
					Message: fmt.Sprintf("SubjectSet '%s' references the '%s' namespace which is undefined. If this namespace was recently added, please try again in a couple minutes", subjectSet, subjectSet.Namespace),
					Type:    namespacemgr.NamespaceDoesntExist,
				}.ToStatus().Err(),
			},
			mockController: func(store *MockRelationTupleStore, nsmanager *MockNamespaceManager) {
				nsmanager.EXPECT().TopChanges(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(func(ctx context.Context, n uint) (namespacemgr.ChangelogIterator, error) {
					changelog := []*namespacemgr.NamespaceChangelogEntry{
						{
							Namespace: namespace1Config.Name,
							Operation: namespacemgr.AddNamespace,
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
				err: namespacemgr.NamespaceConfigError{
					Message: fmt.Sprintf("SubjectSet '%s' references relation '%s' which is undefined in the namespace '%s' at snapshot config timestamp '%s'. If this relation was recently added to the config, please try again in a couple minutes", subjectSet, "relation2", subjectSet.Namespace, timestamp1.Round(0).UTC()),
					Type:    namespacemgr.NamespaceRelationUndefined,
				}.ToStatus().Err(),
			},
			mockController: func(store *MockRelationTupleStore, nsmanager *MockNamespaceManager) {
				nsmanager.EXPECT().TopChanges(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(func(ctx context.Context, n uint) (namespacemgr.ChangelogIterator, error) {
					changelog := []*namespacemgr.NamespaceChangelogEntry{
						{
							Namespace: namespace1Config.Name,
							Operation: namespacemgr.AddNamespace,
							Config:    namespace1Config,
							Timestamp: timestamp1,
						},
						{
							Namespace: namespace2Config.Name,
							Operation: namespacemgr.AddNamespace,
							Config:    namespace2Config,
							Timestamp: timestamp1,
						},
					}
					iter := NewMockChangelogIterator(changelog)
					return iter, nil
				})
			},
		},
		{
			name: "Test-6: Empty Namespace Specified",
			input: &aclpb.WriteRelationTuplesTxnRequest{
				RelationTupleDeltas: []*aclpb.RelationTupleDelta{
					{
						Action:        aclpb.RelationTupleDelta_ACTION_INSERT,
						RelationTuple: &aclpb.RelationTuple{},
					},
				},
			},
			output: output{
				err: status.Errorf(codes.InvalidArgument, "invalid mutation at index '0' - 'namespace' is a required field and cannot be empty"),
			},
		},
		{
			name: "Test-7: Empty Object Specified",
			input: &aclpb.WriteRelationTuplesTxnRequest{
				RelationTupleDeltas: []*aclpb.RelationTupleDelta{
					{
						Action: aclpb.RelationTupleDelta_ACTION_INSERT,
						RelationTuple: &aclpb.RelationTuple{
							Namespace: "namespace1",
						},
					},
				},
			},
			output: output{
				err: status.Errorf(codes.InvalidArgument, "invalid mutation at index '0' - 'object' is a required field and cannot be empty"),
			},
		},
		{
			name: "Test-8: Empty Relation Specified",
			input: &aclpb.WriteRelationTuplesTxnRequest{
				RelationTupleDeltas: []*aclpb.RelationTupleDelta{
					{
						Action: aclpb.RelationTupleDelta_ACTION_INSERT,
						RelationTuple: &aclpb.RelationTuple{
							Namespace: "namespace1",
							Object:    "object1",
						},
					},
				},
			},
			output: output{
				err: status.Errorf(codes.InvalidArgument, "invalid mutation at index '0' - 'relation' is a required field and cannot be empty"),
			},
		},
		{
			name: "Test-9: Empty Subject Specified",
			input: &aclpb.WriteRelationTuplesTxnRequest{
				RelationTupleDeltas: []*aclpb.RelationTupleDelta{
					{
						Action: aclpb.RelationTupleDelta_ACTION_INSERT,
						RelationTuple: &aclpb.RelationTuple{
							Namespace: "namespace1",
							Object:    "object1",
							Relation:  "relation1",
						},
					},
				},
			},
			output: output{
				err: status.Errorf(codes.InvalidArgument, "invalid mutation at index '0' - 'subject' is a required field and cannot be nil"),
			},
		},
		{
			name: "Test-10: No Mutations Included",
			input: &aclpb.WriteRelationTuplesTxnRequest{
				RelationTupleDeltas: []*aclpb.RelationTupleDelta{},
			},
			output: output{
				err: status.Error(codes.InvalidArgument, "one or more mutations must be provided"),
			},
		},
		{
			name: "Test-11: Alias Relation is Implicitly Defined",
			input: &aclpb.WriteRelationTuplesTxnRequest{
				RelationTupleDeltas: []*aclpb.RelationTupleDelta{
					{
						Action: aclpb.RelationTupleDelta_ACTION_INSERT,
						RelationTuple: &aclpb.RelationTuple{
							Namespace: namespace1Config.Name,
							Object:    "object1",
							Relation:  "relation1",
							Subject: &aclpb.Subject{
								Ref: &aclpb.Subject_Set{
									Set: &aclpb.SubjectSet{
										Namespace: namespace1Config.Name,
										Object:    "object2",
										Relation:  "..."},
								},
							},
						},
					},
				},
			},
			output: output{
				response: &aclpb.WriteRelationTuplesTxnResponse{},
			},
			mockController: func(store *MockRelationTupleStore, nsmanager *MockNamespaceManager) {
				nsmanager.EXPECT().TopChanges(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(func(ctx context.Context, n uint) (namespacemgr.ChangelogIterator, error) {
					changelog := []*namespacemgr.NamespaceChangelogEntry{
						{
							Namespace: namespace1Config.Name,
							Operation: namespacemgr.AddNamespace,
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
							Subject: &SubjectSet{
								Namespace: namespace1Config.Name,
								Object:    "object2",
								Relation:  "...",
							},
						},
					},
					[]*InternalRelationTuple{}).Return(nil)
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
			} else {
				mockNamespaceManager.EXPECT().TopChanges(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(func(ctx context.Context, n uint) (namespacemgr.ChangelogIterator, error) {
					changelog := []*namespacemgr.NamespaceChangelogEntry{}
					iter := NewMockChangelogIterator(changelog)
					return iter, nil
				})
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
				t.Errorf("Expected error '%v', but got '%v'", test.output.err, err)
			}

			if !proto.Equal(response, test.output.response) {
				t.Errorf("Expected response '%v', but got '%v'", test.output.response, response)
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
			name: "Test:1  ListRelationTuplesRequest.Query.Namespace undefined",
			input: &aclpb.ListRelationTuplesRequest{
				Query: &aclpb.ListRelationTuplesRequest_Query{Namespace: "undefined"},
			},
			output: output{
				err: status.Error(codes.InvalidArgument, "'undefined' namespace is undefined. If you recently added it, it may take a couple minutes to propagate"),
			},
			mockController: func(store *MockRelationTupleStore, nsmanager *MockNamespaceManager) {
				nsmanager.EXPECT().TopChanges(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(func(ctx context.Context, n uint) (namespacemgr.ChangelogIterator, error) {
					changelog := []*namespacemgr.NamespaceChangelogEntry{}
					iter := NewMockChangelogIterator(changelog)
					return iter, nil
				})
			},
		},
		{
			name: "Test:2 Store Error",
			input: &aclpb.ListRelationTuplesRequest{
				Query: &aclpb.ListRelationTuplesRequest_Query{Namespace: "namespace1"},
			},
			output: output{
				err: internalErrorStatus,
			},
			mockController: func(store *MockRelationTupleStore, nsmanager *MockNamespaceManager) {
				nsmanager.EXPECT().TopChanges(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(func(ctx context.Context, n uint) (namespacemgr.ChangelogIterator, error) {
					changelog := []*namespacemgr.NamespaceChangelogEntry{
						{
							Namespace: namespace1Config.Name,
							Operation: namespacemgr.AddNamespace,
							Config:    namespace1Config,
							Timestamp: time.Now(),
						},
					}
					iter := NewMockChangelogIterator(changelog)
					return iter, nil
				})

				store.EXPECT().ListRelationTuples(gomock.Any(), gomock.Any()).Return(nil, dbError)
			},
		},
		{
			name: "Test:3 Successful Response",
			input: &aclpb.ListRelationTuplesRequest{
				Query: &aclpb.ListRelationTuplesRequest_Query{Namespace: "namespace1"},
			},
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
				nsmanager.EXPECT().TopChanges(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(func(ctx context.Context, n uint) (namespacemgr.ChangelogIterator, error) {
					changelog := []*namespacemgr.NamespaceChangelogEntry{
						{
							Namespace: namespace1Config.Name,
							Operation: namespacemgr.AddNamespace,
							Config:    namespace1Config,
							Timestamp: time.Now(),
						},
					}
					iter := NewMockChangelogIterator(changelog)
					return iter, nil
				})

				store.EXPECT().ListRelationTuples(gomock.Any(), gomock.Any()).Return(
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
		{
			name:  "Test:4 ListRelationTuplesRequest.Query undefined",
			input: &aclpb.ListRelationTuplesRequest{},
			output: output{
				err: status.Error(codes.InvalidArgument, "'query' is a required field and cannot be nil"),
			},
			mockController: func(store *MockRelationTupleStore, nsmanager *MockNamespaceManager) {
				nsmanager.EXPECT().TopChanges(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(func(ctx context.Context, n uint) (namespacemgr.ChangelogIterator, error) {
					changelog := []*namespacemgr.NamespaceChangelogEntry{}
					iter := NewMockChangelogIterator(changelog)
					return iter, nil
				})
			},
		},
		{
			name: "Test:5 ListRelationTuplesRequest.Query.Namespace undefined",
			input: &aclpb.ListRelationTuplesRequest{
				Query: &aclpb.ListRelationTuplesRequest_Query{},
			},
			output: output{
				err: status.Error(codes.InvalidArgument, "'query.namespace' is a required field and cannot be empty"),
			},
			mockController: func(store *MockRelationTupleStore, nsmanager *MockNamespaceManager) {
				nsmanager.EXPECT().TopChanges(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(func(ctx context.Context, n uint) (namespacemgr.ChangelogIterator, error) {
					changelog := []*namespacemgr.NamespaceChangelogEntry{}
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

			response, err := controller.ListRelationTuples(context.Background(), test.input)

			if !errors.Is(err, test.output.err) {
				t.Errorf("Expected error '%v', but got '%v'", test.output.err, err)
			}

			if !proto.Equal(response, test.output.response) {
				t.Errorf("Expected response '%v', but got '%v'", test.output.response, response)
			}
		})
	}
}

func TestAccessController_WriteConfig(t *testing.T) {

	type input struct {
		ctx     context.Context
		request *aclpb.WriteConfigRequest
	}

	type output struct {
		response *aclpb.WriteConfigResponse
		err      error
	}

	ctx1 := context.Background()

	var wrapTxnFuncType = reflect.TypeOf((func(context.Context) error)(nil))

	dbError := errors.New("db error")

	config1 := &aclpb.NamespaceConfig{
		Name: "namespace1",
		Relations: []*aclpb.Relation{
			{Name: "relation1"},
		},
	}

	validRequest := &aclpb.WriteConfigRequest{
		Config: config1,
	}

	tests := []struct {
		name string
		input
		output
		mockController func(nsmanager *MockNamespaceManager)
	}{
		{
			name: "Test-1: Database Error",
			input: input{
				request: validRequest,
			},
			output: output{
				err: internalErrorStatus,
			},
			mockController: func(nsmanager *MockNamespaceManager) {

				gomock.InOrder(
					nsmanager.EXPECT().WrapTransaction(gomock.Any(), gomock.AssignableToTypeOf(wrapTxnFuncType)).DoAndReturn(func(ctx context.Context, fn func(ctx context.Context) error) error {
						return fn(ctx)
					}),
					nsmanager.EXPECT().GetConfig(gomock.Any(), validRequest.Config.Name).Return(nil, dbError),
				)
			},
		},
		{
			name: "Test-2: Successful Upsert",
			input: input{
				ctx:     ctx1,
				request: validRequest,
			},
			output: output{
				response: &aclpb.WriteConfigResponse{},
			},
			mockController: func(nsmanager *MockNamespaceManager) {

				gomock.InOrder(
					nsmanager.EXPECT().WrapTransaction(ctx1, gomock.AssignableToTypeOf(wrapTxnFuncType)).DoAndReturn(func(ctx context.Context, fn func(ctx context.Context) error) error {
						return fn(ctx)
					}),
					nsmanager.EXPECT().GetConfig(ctx1, validRequest.Config.Name).Return(&aclpb.NamespaceConfig{
						Name: config1.Name,
						Relations: []*aclpb.Relation{
							{Name: "other"},
						},
					}, nil),
					nsmanager.EXPECT().LookupRelationReferencesByCount(ctx1, validRequest.Config.Name, []interface{}{"other"}...).Return(map[string]int{}, nil),
					nsmanager.EXPECT().UpsertConfig(ctx1, validRequest.Config).Return(nil),
				)
			},
		},
		{
			name:  "Test-3: WriteConfigRequest.Config undefined",
			input: input{request: &aclpb.WriteConfigRequest{}},
			output: output{
				err: status.Error(codes.InvalidArgument, "'config' is a required field and cannot be nil"),
			},
		},
		{
			name: "Test-4: WriteConfigRequest.Config.Name undefined",
			input: input{
				request: &aclpb.WriteConfigRequest{
					Config: &aclpb.NamespaceConfig{},
				},
			},
			output: output{
				err: status.Error(codes.InvalidArgument, "'config.name' is a required field and cannot be empty"),
			},
		},
		{
			name: "Test-5: Removed Relation with References (InvalidArgument)",
			input: input{
				ctx:     ctx1,
				request: validRequest,
			},
			output: output{
				err: status.Error(codes.InvalidArgument, "Relation(s) [relation1] cannot be removed while one or more relation tuples reference them. Please migrate all relation tuples before removing a relation."),
			},
			mockController: func(nsmanager *MockNamespaceManager) {

				gomock.InOrder(
					nsmanager.EXPECT().WrapTransaction(ctx1, gomock.AssignableToTypeOf(wrapTxnFuncType)).DoAndReturn(func(ctx context.Context, fn func(ctx context.Context) error) error {
						return fn(ctx)
					}),

					nsmanager.EXPECT().GetConfig(ctx1, validRequest.Config.Name).Return(&aclpb.NamespaceConfig{
						Name: config1.Name,
						Relations: []*aclpb.Relation{
							{Name: "other"},
						},
					}, nil),
					nsmanager.EXPECT().LookupRelationReferencesByCount(ctx1, validRequest.Config.Name, []interface{}{"other"}...).Return(map[string]int{
						"relation1": 1,
					}, nil),
				)
			},
		},
		{
			name: "Test-6: Unexpected Rewrite Operation",
			input: input{
				request: &aclpb.WriteConfigRequest{
					Config: &aclpb.NamespaceConfig{
						Name: "namespace1",
						Relations: []*aclpb.Relation{
							{
								Name: "relation1",
								Rewrite: &aclpb.Rewrite{
									RewriteOperation: nil,
								},
							},
						},
					},
				},
			},
			output: output{
				err: status.Error(codes.InvalidArgument, "unexpected rewrite operation - 'union' or 'intersection' expected"),
			},
		},
		{
			name: "Test-7: Unexpected Rewrite Set Operation Child Type",
			input: input{
				request: &aclpb.WriteConfigRequest{
					Config: &aclpb.NamespaceConfig{
						Name: "namespace1",
						Relations: []*aclpb.Relation{
							{
								Name: "relation1",
								Rewrite: &aclpb.Rewrite{
									RewriteOperation: &aclpb.Rewrite_Intersection{
										Intersection: &aclpb.SetOperation{
											Children: []*aclpb.SetOperation_Child{
												{ChildType: nil},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			output: output{
				err: status.Error(codes.InvalidArgument, "unexpected rewrite operation child - 'this', 'computedSubjectset', 'tupleToSubjectset', or 'rewrite' expected"),
			},
		},
		{
			name: "Test-8: Undefined ComputedSubjectSet relation",
			input: input{
				request: &aclpb.WriteConfigRequest{
					Config: &aclpb.NamespaceConfig{
						Name: "namespace1",
						Relations: []*aclpb.Relation{
							{
								Name: "relation1",
								Rewrite: &aclpb.Rewrite{
									RewriteOperation: &aclpb.Rewrite_Intersection{
										Intersection: &aclpb.SetOperation{
											Children: []*aclpb.SetOperation_Child{
												{ChildType: &aclpb.SetOperation_Child_Rewrite{
													Rewrite: &aclpb.Rewrite{
														RewriteOperation: &aclpb.Rewrite_Union{
															Union: &aclpb.SetOperation{
																Children: []*aclpb.SetOperation_Child{
																	{ChildType: &aclpb.SetOperation_Child_ComputedSubjectset{
																		ComputedSubjectset: &aclpb.ComputedSubjectset{
																			Relation: "relation2",
																		},
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
						},
					},
				},
			},
			output: output{
				err: status.Error(codes.InvalidArgument, "'relation2' relation is referenced but undefined in the provided namespace config"),
			},
		},
		{
			name: "Test-9: Undefined ComputedSubjectSet relation",
			input: input{
				request: &aclpb.WriteConfigRequest{
					Config: &aclpb.NamespaceConfig{
						Name: "namespace1",
						Relations: []*aclpb.Relation{
							{
								Name: "relation1",
								Rewrite: &aclpb.Rewrite{
									RewriteOperation: &aclpb.Rewrite_Intersection{
										Intersection: &aclpb.SetOperation{
											Children: []*aclpb.SetOperation_Child{
												{ChildType: &aclpb.SetOperation_Child_TupleToSubjectset{
													TupleToSubjectset: &aclpb.TupleToSubjectset{
														Tupleset: &aclpb.TupleToSubjectset_Tupleset{
															Relation: "relation2",
														},
													},
												}},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			output: output{
				err: status.Error(codes.InvalidArgument, "'relation2' relation is referenced but undefined in the provided namespace config"),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {

			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockNamespaceManager := NewMockNamespaceManager(ctrl)

			mockNamespaceManager.EXPECT().TopChanges(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(func(ctx context.Context, n uint) (namespacemgr.ChangelogIterator, error) {
				changelog := []*namespacemgr.NamespaceChangelogEntry{}
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

			response, err := controller.WriteConfig(test.input.ctx, test.input.request)

			if !errors.Is(err, test.output.err) {
				t.Errorf("Expected error '%v', but got '%v'", test.output.err, err)
			}

			if !proto.Equal(response, test.output.response) {
				t.Errorf("Expected response '%v', but got '%v'", test.output.response, response)
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
				err: internalErrorStatus,
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
		{
			name:  "Test-3: ReadConfigRequest.Namespace undefined",
			input: &aclpb.ReadConfigRequest{},
			output: output{
				err: status.Error(codes.InvalidArgument, "'namespace' is a required field and cannot be empty"),
			},
		},
		{
			name:  "Test-4: Namespace NotFound",
			input: validRequest,
			output: output{
				err: status.Errorf(codes.NotFound, "The namespace '%s' does not exist. If it was recently added, please try again in a couple of minutes", validRequest.Namespace),
			},
			mockController: func(nsmanager *MockNamespaceManager) {
				nsmanager.EXPECT().GetConfig(gomock.Any(), validRequest.Namespace).Return(nil, namespacemgr.ErrNamespaceDoesntExist)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {

			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockNamespaceManager := NewMockNamespaceManager(ctrl)

			mockNamespaceManager.EXPECT().TopChanges(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(func(ctx context.Context, n uint) (namespacemgr.ChangelogIterator, error) {
				changelog := []*namespacemgr.NamespaceChangelogEntry{}
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
				t.Errorf("Expected error '%v', but got '%v'", test.output.err, err)
			}

			if !proto.Equal(response, test.output.response) {
				t.Errorf("Expected response '%v', but got '%v'", test.output.response, response)
			}
		})
	}
}

func TestAccessController_HealthCheck(t *testing.T) {
	controller := AccessController{}

	resp, err := controller.HealthCheck(context.Background(), &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		t.Fatal("Expected nil error but got non-nil")
	}

	expected := &grpc_health_v1.HealthCheckResponse{
		Status: grpc_health_v1.HealthCheckResponse_SERVING,
	}
	if !proto.Equal(resp, expected) {
		t.Errorf("Expected response '%v', but got '%v'", expected, resp)
	}
}

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

	mockNamespaceManager.EXPECT().TopChanges(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(func(ctx context.Context, n uint) (namespacemgr.ChangelogIterator, error) {
		changelog := []*namespacemgr.NamespaceChangelogEntry{
			{
				Namespace: config1.Name,
				Operation: namespacemgr.AddNamespace,
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

func TestAccessController_GetBroadcasts(t *testing.T) {

	controller := &AccessController{}

	if controller.GetBroadcasts(0, 0) != nil {
		t.Error("Expected nil, but got non-nil")
	}
}

func TestAccessController_NodeMeta(t *testing.T) {

	controller := AccessController{
		NodeConfigs: NodeConfigs{
			ServerPort: 50052,
		},
	}

	expected, err := json.Marshal(NodeMetadata{
		ServerPort: 50052,
	})
	if err != nil {
		t.Fatalf("Failed to json.Marshal the expected NodeMetadata: %v", err)
	}

	meta := controller.NodeMeta(0)

	if !reflect.DeepEqual(meta, expected) {
		t.Errorf("Expected metadata '%s', but got '%s'", expected, meta)
	}
}

func TestAccessController_watchNamespaceConfigs(t *testing.T) {

	timestamp := time.Now()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockNamespaceManager := NewMockNamespaceManager(ctrl)
	mockPeerStore := NewMockPeerNamespaceConfigStore(ctrl)

	controller := &AccessController{
		NodeConfigs: NodeConfigs{
			ServerID: "server1",
		},
		NamespaceManager:         mockNamespaceManager,
		PeerNamespaceConfigStore: mockPeerStore,
		shutdown:                 make(chan struct{}),
	}

	mockNamespaceManager.EXPECT().TopChanges(gomock.Any(), uint(3)).DoAndReturn(func(ctx context.Context, n uint) (namespacemgr.ChangelogIterator, error) {
		changelog := []*namespacemgr.NamespaceChangelogEntry{
			{
				Namespace: dirsConfig.Name,
				Operation: namespacemgr.AddNamespace,
				Config:    dirsConfig,
				Timestamp: timestamp,
			},
			{
				Namespace: filesConfig.Name,
				Operation: namespacemgr.UpdateNamespace,
				Config:    filesConfig,
				Timestamp: timestamp,
			},
		}
		iter := NewMockChangelogIterator(changelog)
		return iter, nil
	}).After(
		mockNamespaceManager.EXPECT().TopChanges(gomock.Any(), uint(3)).DoAndReturn(func(ctx context.Context, n uint) (namespacemgr.ChangelogIterator, error) {
			return nil, fmt.Errorf("some database error")
		}),
	)

	mockPeerStore.EXPECT().SetNamespaceConfigSnapshot(controller.ServerID, dirsConfig.Name, dirsConfig, timestamp).Return(nil)
	mockPeerStore.EXPECT().SetNamespaceConfigSnapshot(controller.ServerID, filesConfig.Name, filesConfig, timestamp).DoAndReturn(
		func(peerID, namespace string, config *aclpb.NamespaceConfig, ts time.Time) error {
			controller.shutdown <- struct{}{}
			return nil
		},
	)

	controller.watchNamespaceConfigs(context.Background())
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

type mockChangelogIterator struct {
	index int
	buf   []*namespacemgr.NamespaceChangelogEntry
}

func NewMockChangelogIterator(changelog []*namespacemgr.NamespaceChangelogEntry) namespacemgr.ChangelogIterator {

	m := mockChangelogIterator{
		index: 0,
		buf:   changelog,
	}

	return &m
}

func (m *mockChangelogIterator) Next() bool {
	return m.index < len(m.buf)
}

func (m *mockChangelogIterator) Value() (*namespacemgr.NamespaceChangelogEntry, error) {
	entry := m.buf[m.index]
	m.index += 1
	return entry, nil
}

func (m *mockChangelogIterator) Close(ctx context.Context) error {
	return nil
}
