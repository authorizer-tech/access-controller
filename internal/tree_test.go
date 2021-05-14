package accesscontroller

import (
	"testing"

	aclpb "github.com/authorizer-tech/access-controller/gen/go/authorizer-tech/accesscontroller/v1alpha1"
	"google.golang.org/protobuf/proto"
)

func TestTree_ToProto(t *testing.T) {

	tests := []struct {
		input  *Tree
		output *aclpb.SubjectTree
	}{
		{
			output: nil,
		},
		{
			input: &Tree{
				Type: UnionNode,
				Subject: &SubjectSet{
					Namespace: "groups",
					Object:    "group1",
					Relation:  "member",
				},
				Children: []*Tree{
					{
						Type:    LeafNode,
						Subject: &SubjectID{"user1"},
					},
				},
			},
			output: &aclpb.SubjectTree{
				NodeType: aclpb.NodeType_NODE_TYPE_UNION,
				Subject: &aclpb.Subject{
					Ref: &aclpb.Subject_Set{
						Set: &aclpb.SubjectSet{
							Namespace: "groups",
							Object:    "group1",
							Relation:  "member",
						},
					},
				},
				Children: []*aclpb.SubjectTree{
					{
						NodeType: aclpb.NodeType_NODE_TYPE_LEAF,
						Subject: &aclpb.Subject{
							Ref: &aclpb.Subject_Id{Id: "user1"},
						},
					},
				},
			},
		},
	}

	for _, test := range tests {
		actual := test.input.ToProto()

		if !proto.Equal(actual, test.output) {
			t.Errorf("Expected '%v', but got '%v'", test.output, actual)
		}
	}
}

func TestNodeType_ToProto(t *testing.T) {

	tests := []struct {
		input  NodeType
		output aclpb.NodeType
	}{
		{
			input:  UnionNode,
			output: aclpb.NodeType_NODE_TYPE_UNION,
		},
		{
			input:  IntersectionNode,
			output: aclpb.NodeType_NODE_TYPE_INTERSECTION,
		},
		{
			input:  LeafNode,
			output: aclpb.NodeType_NODE_TYPE_LEAF,
		},
		{
			input:  NodeType("unspecified-type"),
			output: aclpb.NodeType_NODE_TYPE_UNSPECIFIED,
		},
	}

	for _, test := range tests {
		proto := test.input.ToProto()

		if proto != test.output {
			t.Errorf("Expected '%v', but got '%v'", test.output, proto)
		}
	}
}
