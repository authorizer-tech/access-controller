package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"google.golang.org/protobuf/encoding/protojson"

	aclpb "github.com/authorizer-tech/access-controller/genprotos/authorizer/accesscontroller/v1alpha1"
	namespacemgr "github.com/authorizer-tech/access-controller/internal/namespace-manager"
)

type txnKey struct{}

type sqlNamespaceManager struct {
	db *sql.DB
}

// NewNamespaceManager instantiates a namespace manager that is backed by postgres
// for persistence.
func NewNamespaceManager(db *sql.DB) (namespacemgr.NamespaceManager, error) {

	m := sqlNamespaceManager{
		db,
	}

	return &m, nil
}

// UpsertConfig upserts the provided namespace config. If the namespace config already exists, the existing
// one is overwritten with the new cfg. Otherwise the cfg is added to the list of namespace configs.
func (m *sqlNamespaceManager) UpsertConfig(ctx context.Context, cfg *aclpb.NamespaceConfig) error {

	jsonConfig, err := protojson.Marshal(cfg)
	if err != nil {
		return err
	}

	err = m.withTransaction(ctx, func(ctx context.Context, txn *sql.Tx) error {

		row := txn.QueryRow(`SELECT COUNT(namespace) FROM "namespace-configs" WHERE namespace=$1`, cfg.GetName())

		var count int
		if err := row.Scan(&count); err != nil {
			return err
		}

		var operation namespacemgr.NamespaceOperation
		if count > 0 {
			operation = namespacemgr.UpdateNamespace
		} else {
			operation = namespacemgr.AddNamespace

			stmt := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
				object text,
				relation text,
				subject text,
				PRIMARY KEY (object, relation, subject)
			)`, cfg.GetName())

			_, err := txn.Exec(stmt)
			if err != nil {
				return err
			}
		}

		ins1 := goqu.Insert("namespace-configs").
			Cols("namespace", "config", "timestamp").
			Vals(
				goqu.Vals{cfg.GetName(), jsonConfig, goqu.L("NOW()")},
			)

		ins2 := goqu.Insert("namespace-changelog").
			Cols("namespace", "operation", "config", "timestamp").
			Vals(
				goqu.Vals{cfg.GetName(), operation, jsonConfig, goqu.L("NOW()")}, // todo: make sure this is an appropriate txn commit timestamp
			)

		sql1, args1, err := ins1.ToSQL()
		if err != nil {
			return err
		}

		sql2, args2, err := ins2.ToSQL()
		if err != nil {
			return err
		}

		_, err = txn.Exec(sql1, args1...)
		if err != nil {
			return err
		}

		_, err = txn.Exec(sql2, args2...)
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

// GetConfig fetches the latest namespace configuration snapshot. If no namespace config exists for the provided
// namespace 'nil' is returned. If any error occurs with the underlying txn an error is returned.
func (m *sqlNamespaceManager) GetConfig(ctx context.Context, namespace string) (*aclpb.NamespaceConfig, error) {

	query := `SELECT config FROM "namespace-configs" WHERE namespace = $1 AND timestamp = (SELECT MAX(timestamp) FROM "namespace-configs" WHERE namespace = $1)`

	var jsonConfig string

	err := m.withTransaction(ctx, func(ctx context.Context, txn *sql.Tx) error {

		row := txn.QueryRow(query, namespace)

		if err := row.Scan(&jsonConfig); err != nil {
			if err == sql.ErrNoRows {
				return namespacemgr.ErrNamespaceDoesntExist
			}

			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	var config aclpb.NamespaceConfig
	if err := protojson.Unmarshal([]byte(jsonConfig), &config); err != nil {
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
								{ChildType: &aclpb.SetOperation_Child_This_{}},
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
func (m *sqlNamespaceManager) TopChanges(ctx context.Context, n uint) (namespacemgr.ChangelogIterator, error) {

	sql := `
	SELECT "namespace", "operation", "config", "timestamp" FROM "namespace-changelog" AS "cfg1" 
	WHERE "timestamp" IN (SELECT "timestamp" FROM "namespace-changelog" AS "cfg2" 
	WHERE ("cfg1"."namespace" = "cfg2"."namespace") ORDER BY "timestamp" DESC LIMIT $1) 
	ORDER BY "namespace" ASC, "timestamp" ASC`

	rows, err := m.db.Query(sql, n)
	if err != nil {
		return nil, err
	}

	i := &iterator{rows}

	return i, nil
}

// LookupRelationReferencesByCount does a reverse-lookup for a one or more namespace config relations and
// returns a map whose keys are the relation and whose value is a count indicating the number of times that
// relation was referenced by a relation tuple.
func (m *sqlNamespaceManager) LookupRelationReferencesByCount(ctx context.Context, namespace string, relations ...string) (map[string]int, error) {

	references := map[string]int{}

	builder := goqu.Dialect("postgres").From("namespace-relation-lookup").
		Select(
			"relation",
			goqu.COUNT("*"),
		).
		Where(goqu.Ex{
			"namespace": namespace,
			"relation":  relations,
		}).
		GroupBy("relation")

	query, args, err := builder.ToSQL()
	if err != nil {
		return nil, err
	}

	err = m.withTransaction(ctx, func(ctx context.Context, txn *sql.Tx) error {

		rows, err := txn.Query(query, args...)
		if err != nil {
			return err
		}

		for rows.Next() {
			var relation string
			var count int

			if err := rows.Scan(&relation, &count); err != nil {
				return err
			}

			references[relation] = count
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return references, nil
}

// WrapTransaction wraps the fn inside a single transaction. If fn returns an error, the transaction is aborted
// and rolled back.
func (m *sqlNamespaceManager) WrapTransaction(ctx context.Context, fn func(ctx context.Context) error) error {

	var err error

	txn, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	defer func() {
		if err == nil {
			return
		}

		err = txn.Rollback()
	}()

	ctx = context.WithValue(ctx, txnKey{}, txn)

	if err = fn(ctx); err != nil {
		return err
	}

	if err = txn.Commit(); err != nil {
		return err
	}

	return err
}

// withTransaction wraps fn in a transaction and commits it if and only if it is not already part of another
// transaction.
func (m *sqlNamespaceManager) withTransaction(ctx context.Context, fn func(ctx context.Context, txn *sql.Tx) error) error {

	var txn *sql.Tx
	var err error

	if val := ctx.Value(txnKey{}); val != nil {
		txn = val.(*sql.Tx)
	}

	if txn == nil {
		txn, err = m.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}

		defer func() {
			err = txn.Commit()
		}()
	}

	if err := fn(ctx, txn); err != nil {
		return err
	}

	return err
}

// iterator provides an implementation of a ChangelogIterator ontop of a standard
// pgx.Rows iterator.
type iterator struct {
	rows *sql.Rows
}

// Next prepares the next row for reading. It returns true if there is another row and false if no more rows are available.
func (i *iterator) Next() bool {
	return i.rows.Next()
}

// Value reads the current ChangelogEntry value the iterator is pointing at. Any read
// errors are returned immediately.
func (i *iterator) Value() (*namespacemgr.NamespaceChangelogEntry, error) {

	var namespace, operation, configJSON string
	var timestamp time.Time
	if err := i.rows.Scan(&namespace, &operation, &configJSON, &timestamp); err != nil {
		return nil, err
	}

	var op namespacemgr.NamespaceOperation
	switch operation {
	case "ADD":
		op = namespacemgr.AddNamespace
	case "UPDATE":
		op = namespacemgr.UpdateNamespace
	default:
		panic("An unexpected namespace operation was encountered. Underlying system invariants were not met!")
	}

	var cfg aclpb.NamespaceConfig
	if err := protojson.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return nil, err
	}

	entry := &namespacemgr.NamespaceChangelogEntry{
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

// Always verify that we implement the interface
var _ namespacemgr.NamespaceManager = &sqlNamespaceManager{}
