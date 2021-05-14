package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/jackc/pgx/v4"
	"github.com/jackc/pgx/v4/pgxpool"
	"google.golang.org/protobuf/encoding/protojson"

	aclpb "github.com/authorizer-tech/access-controller/gen/go/authorizer-tech/accesscontroller/v1alpha1"
	ac "github.com/authorizer-tech/access-controller/internal"
)

type sqlNamespaceManager struct {
	db *pgxpool.Pool
}

// NewNamespaceManager instantiates a namespace manager that is backed by postgres
// for persistence.
func NewNamespaceManager(db *pgxpool.Pool) (ac.NamespaceManager, error) {

	m := sqlNamespaceManager{
		db: db,
	}

	return &m, nil
}

// WriteConfig upserts the provided namespace configuration. On conflict, the existing namespace
// configuration is overwritten.
func (m *sqlNamespaceManager) WriteConfig(ctx context.Context, cfg *aclpb.NamespaceConfig) error {

	jsonConfig, err := json.Marshal(cfg)
	if err != nil {
		return err
	}

	txn, err := m.db.Begin(ctx)
	if err != nil {
		return err
	}

	ins1 := goqu.Insert("namespace-configs").
		Cols("namespace", "config", "timestamp").
		Vals(
			goqu.Vals{cfg.Name, jsonConfig, goqu.L("NOW()")},
		)

	ins2 := goqu.Insert("namespace-changelog").
		Cols("namespace", "operation", "config", "commited").
		Vals(
			goqu.Vals{cfg.Name, ac.UpdateNamespace, jsonConfig, goqu.L("NOW()")}, // todo: make sure this is an appropriate txn commit timestamp
		)

	sql1, args1, err := ins1.ToSQL()
	if err != nil {
		return err
	}

	sql2, args2, err := ins2.ToSQL()
	if err != nil {
		return err
	}

	_, err = txn.Exec(ctx, sql1, args1...)
	if err != nil {
		return err
	}

	_, err = txn.Exec(ctx, sql2, args2...)
	if err != nil {
		return err
	}

	return txn.Commit(ctx)
}

// GetConfig fetches the latest namespace configuration snapshot. If no namespace config exists for the provided
// namespace 'nil' is returned. If any error occurs with the underlying txn an error is returned.
func (m *sqlNamespaceManager) GetConfig(ctx context.Context, namespace string) (*aclpb.NamespaceConfig, error) {

	sql := `SELECT config FROM "namespace-configs" WHERE namespace = $1 AND timestamp = (SELECT MAX(timestamp) FROM "namespace-configs" WHERE namespace = $1)`

	row := m.db.QueryRow(ctx, sql, namespace)

	var jsonConfig string
	if err := row.Scan(&jsonConfig); err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}

		return nil, err
	}

	var config aclpb.NamespaceConfig
	if err := json.Unmarshal([]byte(jsonConfig), &config); err != nil {
		return nil, err
	}

	return &config, nil
}

// GetRewrite fetches the latest namespace config rewrite for the (namespace, relation) pair. If no namespace
// config exists for the provided namespace 'nil' is returned. If any error occurs with the underlying txn
// an error is returned.
func (m *sqlNamespaceManager) GetRewrite(ctx context.Context, namespace, relation string) (*aclpb.Rewrite, error) {

	cfg, err := m.GetConfig(ctx, namespace)
	if err != nil {
		return nil, err
	}

	if cfg == nil {
		return nil, nil
	}

	relations := cfg.GetRelations()
	for _, r := range relations {

		if r.GetName() == relation {
			rewrite := r.GetRewrite()

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
	}

	return nil, nil
}

// TopChanges fetches the top n most recent namespace config changes for each namespace. If any error
// occurs with the underlying txn an error is returned. Otherwise an iterator that iterates over the
// changes is returned.
func (m *sqlNamespaceManager) TopChanges(ctx context.Context, n uint) (ac.ChangelogIterator, error) {

	sql := `
	SELECT "namespace", "operation", "config", "commited" FROM "namespace-changelog" AS "cfg1" 
	WHERE "commited" IN (SELECT "commited" FROM "namespace-changelog" AS "cfg2" 
	WHERE ("cfg1"."namespace" = "cfg2"."namespace") ORDER BY "commited" DESC LIMIT $1) 
	ORDER BY "namespace" ASC, "commited" ASC`

	rows, err := m.db.Query(ctx, sql, n)
	if err != nil {
		return nil, err
	}

	i := &iterator{rows}

	return i, nil
}

// iterator provides an implementation of a ChangelogIterator ontop of a standard
// pgx.Rows iterator.
type iterator struct {
	rows pgx.Rows
}

// Next prepares the next row for reading. It returns true if there is another row and false if no more rows are available.
func (i *iterator) Next() bool {
	return i.rows.Next()
}

// Value reads the current ChangelogEntry value the iterator is pointing at. Any read
// errors are returned immediately.
func (i *iterator) Value() (*ac.NamespaceChangelogEntry, error) {

	var namespace, operation, configJson string
	var timestamp time.Time
	if err := i.rows.Scan(&namespace, &operation, &configJson, &timestamp); err != nil {
		return nil, err
	}

	var op ac.NamespaceOperation
	switch operation {
	case "UPDATE":
		op = ac.UpdateNamespace
	default:
		panic("An unexpected namespace operation was encountered. Underlying system invariants were not met!")
	}

	var cfg aclpb.NamespaceConfig
	if err := protojson.Unmarshal([]byte(configJson), &cfg); err != nil {
		return nil, err
	}

	entry := &ac.NamespaceChangelogEntry{
		Namespace: namespace,
		Operation: op,
		Config:    &cfg,
		Timestamp: timestamp,
	}

	return entry, nil
}

// Close closes the iterator and frees up all resources used by it.
func (i *iterator) Close(ctx context.Context) error {
	i.rows.Close()
	return nil
}
