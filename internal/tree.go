package accesscontroller

import (
	"fmt"
	"strings"

	aclpb "github.com/authorizer-tech/access-controller/gen/go/authorizer-tech/accesscontroller/v1alpha1"
)

type NodeType string

const (
	UnionNode        NodeType = "union"
	IntersectionNode NodeType = "intersection"
	LeafNode         NodeType = "leaf"
)

type Tree struct {
	Type NodeType `json:"type"`

	Subject  Subject `json:"subject"`
	Children []*Tree `json:"children,omitempty"`
}

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

func (t *Tree) String() string {
	sub := t.Subject.String()

	if t.Type == LeafNode {
		return fmt.Sprintf("☘ %s️", sub)
	}

	children := make([]string, len(t.Children))
	for i, c := range t.Children {
		children[i] = strings.Join(strings.Split(c.String(), "\n"), "\n│  ")
	}

	return fmt.Sprintf("∪ %s\n├─ %s", sub, strings.Join(children, "\n├─ "))
}
