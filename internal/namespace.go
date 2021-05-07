package accesscontroller

import (
	"context"

	aclpb "github.com/authorizer-tech/access-controller/gen/go/authorizer-tech/accesscontroller/v1alpha1"
)

type NamespaceManager interface {
	WriteConfig(ctx context.Context, cfg *aclpb.NamespaceConfig) error
	GetConfig(ctx context.Context, namespace string) (*aclpb.NamespaceConfig, error)
	GetRewrite(ctx context.Context, namespace, relation string) (*aclpb.Rewrite, error)
}
