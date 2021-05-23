package accesscontroller

import (
	aclpb "github.com/authorizer-tech/access-controller/genprotos/authorizer/accesscontroller/v1alpha1"
)

// NodeType represents a specific type of node within a SubjectTree structure.
type NodeType string

const (

	// UnionNode represents a SubjectTree node that joins it's children via a union.
	UnionNode NodeType = "union"

	// IntersectionNode represents a SubjectTree node that joins it's children via an intersection.
	IntersectionNode NodeType = "intersection"

	// LeafNode represents a SubjectTree node with no children.
	LeafNode NodeType = "leaf"
)

// ToProto returns the protobuf representation of the NodeType.
func (t NodeType) ToProto() aclpb.NodeType {
	switch t {
	case LeafNode:
		return aclpb.NodeType_NODE_TYPE_LEAF
	case UnionNode:
		return aclpb.NodeType_NODE_TYPE_UNION
	case IntersectionNode:
		return aclpb.NodeType_NODE_TYPE_INTERSECTION
	}
	return aclpb.NodeType_NODE_TYPE_UNSPECIFIED
}

// SubjectTree represents a tree datastructure that stores relationships between Subjects.
type SubjectTree struct {
	Type NodeType `json:"type"`

	Subject  Subject        `json:"subject"`
	Children []*SubjectTree `json:"children,omitempty"`
}

// ToProto returns the protobuf representation of the SubjectTree.
func (t *SubjectTree) ToProto() *aclpb.SubjectTree {
	if t == nil {
		return nil
	}

	if t.Type == LeafNode {
		return &aclpb.SubjectTree{
			NodeType: aclpb.NodeType_NODE_TYPE_LEAF,
			Subject:  t.Subject.ToProto(),
		}
	}

	children := make([]*aclpb.SubjectTree, len(t.Children))
	for i, c := range t.Children {
		children[i] = c.ToProto()
	}

	return &aclpb.SubjectTree{
		NodeType: t.Type.ToProto(),
		Subject:  t.Subject.ToProto(),
		Children: children,
	}
}
