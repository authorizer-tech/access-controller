package accesscontroller

import (
	aclpb "github.com/authorizer-tech/access-controller/genprotos/authorizer/accesscontroller/v1alpha1"
)

type NodeType string

const (
	UnionNode        NodeType = "union"
	IntersectionNode NodeType = "intersection"
	LeafNode         NodeType = "leaf"
)

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

type Tree struct {
	Type NodeType `json:"type"`

	Subject  Subject `json:"subject"`
	Children []*Tree `json:"children,omitempty"`
}

func (t *Tree) ToProto() *aclpb.SubjectTree {
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
