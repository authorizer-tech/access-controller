package inmem

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	aclpb "github.com/authorizer-tech/access-controller/gen/go/authorizer-tech/accesscontroller/v1alpha1"
	ac "github.com/authorizer-tech/access-controller/internal"
	"google.golang.org/protobuf/encoding/protojson"
)

const configExt = "json"

type inmemNamespaceManager struct {
	configs  map[string]*aclpb.NamespaceConfig
	rewrites map[string]*aclpb.Rewrite
}

func NewNamespaceManager(path string) (ac.NamespaceManager, error) {

	configs := make(map[string]*aclpb.NamespaceConfig)
	rewrites := make(map[string]*aclpb.Rewrite)

	if path == "" {
		return nil, fmt.Errorf("An empty path must not be provided")
	}

	err := filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
		if info.IsDir() {
			return nil
		}

		index := strings.LastIndex(p, ".")
		if index == -1 {
			return nil
		}

		ext := p[index+1:]
		if ext != configExt {
			return nil
		}

		blob, err := ioutil.ReadFile(p)
		if err != nil {
			return err
		}

		var config aclpb.NamespaceConfig
		err = protojson.Unmarshal(blob, &config)
		if err != nil {
			return err
		}

		namespace := config.GetName()
		configs[namespace] = &config

		for _, relation := range config.GetRelations() {
			rewrite := relation.GetRewrite()

			if rewrite == nil {
				rewrite = &aclpb.Rewrite{
					RewriteOperation: &aclpb.Rewrite_Union{
						Union: &aclpb.SetOperation{
							Children: []*aclpb.SetOperation_Child{
								{ChildType: &aclpb.SetOperation_Child_XThis{}},
							},
						},
					},
				}
			}

			rewrites[namespace+"#"+relation.GetName()] = rewrite
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	m := inmemNamespaceManager{
		configs,
		rewrites,
	}
	return &m, nil

}

func (i *inmemNamespaceManager) WriteConfig(ctx context.Context, cfg *aclpb.NamespaceConfig) error {

	namespace := cfg.GetName()
	i.configs[namespace] = cfg

	for _, relation := range cfg.GetRelations() {
		key := fmt.Sprintf("%s#%s", namespace, relation.GetName())
		i.rewrites[key] = relation.GetRewrite()
	}

	return nil
}

func (i *inmemNamespaceManager) GetConfig(ctx context.Context, namespace string) (*aclpb.NamespaceConfig, error) {

	config, ok := i.configs[namespace]
	if !ok {
		return nil, fmt.Errorf("No namespace configs found for namespace '%s'", namespace)
	}

	return config, nil
}

func (i *inmemNamespaceManager) GetRewrite(ctx context.Context, namespace, relation string) (*aclpb.Rewrite, error) {

	key := fmt.Sprintf("%s#%s", namespace, relation)
	rewrite, ok := i.rewrites[key]
	if !ok {
		return nil, fmt.Errorf("No rewrite was found for the namespace '%s' and relation '%s'", namespace, relation)
	}

	if rewrite == nil {
		rewrite = &aclpb.Rewrite{
			RewriteOperation: &aclpb.Rewrite_Union{
				Union: &aclpb.SetOperation{
					Children: []*aclpb.SetOperation_Child{
						{ChildType: &aclpb.SetOperation_Child_XThis{}},
					},
				},
			},
		}
	}

	return rewrite, nil
}
