package accesscontroller

//go:generate mockgen -self_package github.com/authorizer-tech/access-controller/internal -destination=./mock_relationstore_test.go -package accesscontroller . RelationTupleStore

import (
	"context"

	aclpb "github.com/authorizer-tech/access-controller/genprotos/authorizer/accesscontroller/v1alpha1"
)

// RelationTupleStore defines an interface to manage the storage of relation tuples.
type RelationTupleStore interface {
	SubjectSets(ctx context.Context, object Object, relations ...string) ([]SubjectSet, error)
	ListRelationTuples(ctx context.Context, query *aclpb.ListRelationTuplesRequest_Query) ([]InternalRelationTuple, error)
	RowCount(ctx context.Context, query RelationTupleQuery) (int64, error)
	TransactRelationTuples(ctx context.Context, insert []*InternalRelationTuple, delete []*InternalRelationTuple) error
}
