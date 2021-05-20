package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"google.golang.org/protobuf/encoding/protojson"

	aclpb "github.com/authorizer-tech/access-controller/gen/go/authorizer-tech/accesscontroller/v1alpha1"
	ac "github.com/authorizer-tech/access-controller/internal"
)

type sqlNamespaceManager struct {
	db *sql.DB
}

// NewNamespaceManager instantiates a namespace manager that is backed by postgres
// for persistence.
func NewNamespaceManager(db *sql.DB) (ac.NamespaceManager, error) {

	m := sqlNamespaceManager{
		db,
	}

	return &m, nil
}

// AddConfig appends the provided namespace configuration. On conflict, an ErrNamespaceAlreadyExists is
// returned.
func (m *sqlNamespaceManager) AddConfig(ctx context.Context, cfg *aclpb.NamespaceConfig) error {

	jsonConfig, err := protojson.Marshal(cfg)
	if err != nil {
		return err
	}

	txn, err := m.db.Begin()
	if err != nil {
		return err
	}

	var count int
	row := txn.QueryRow(`SELECT COUNT(namespace) FROM "namespace-configs" WHERE namespace=$1`, cfg.GetName())
	if err := row.Scan(&count); err != nil {
		return err
	}

	if count > 0 {
		return ac.ErrNamespaceAlreadyExists
	}

	ins1 := goqu.Insert("namespace-configs").
		Cols("namespace", "config", "timestamp").
		Vals(
			goqu.Vals{cfg.Name, jsonConfig, goqu.L("NOW()")},
		)

	ins2 := goqu.Insert("namespace-changelog").
		Cols("namespace", "operation", "config", "timestamp").
		Vals(
			goqu.Vals{cfg.Name, ac.AddNamespace, jsonConfig, goqu.L("NOW()")}, // todo: make sure this is an appropriate txn commit timestamp
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

	stmt := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		object text,
		relation text,
		subject text,
		PRIMARY KEY (object, relation, subject)
	)`, cfg.Name)

	_, err = txn.Exec(stmt)
	if err != nil {
		return err
	}

	return txn.Commit()
}

// GetConfig fetches the latest namespace configuration snapshot. If no namespace config exists for the provided
// namespace 'nil' is returned. If any error occurs with the underlying txn an error is returned.
func (m *sqlNamespaceManager) GetConfig(ctx context.Context, namespace string) (*aclpb.NamespaceConfig, error) {

	query := `SELECT config FROM "namespace-configs" WHERE namespace = $1 AND timestamp = (SELECT MAX(timestamp) FROM "namespace-configs" WHERE namespace = $1)`

	row := m.db.QueryRow(query, namespace)

	var jsonConfig string
	if err := row.Scan(&jsonConfig); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}

		return nil, err
	}

	var config aclpb.NamespaceConfig
	if err := protojson.Unmarshal([]byte(jsonConfig), &config); err != nil {
		return nil, err
	}

	return &config, nil
}

func (m *sqlNamespaceManager) UpsertRelation(ctx context.Context, namespace string, relation *aclpb.Relation) error {

	txn, err := m.db.Begin()
	if err != nil {
		return err
	}

	row := txn.QueryRow(`SELECT config FROM "namespace-configs" WHERE timestamp = (SELECT MAX(timestamp) FROM "namespace-configs" WHERE namespace = $1)`, namespace)

	var cfgStr string
	if err := row.Scan(&cfgStr); err != nil {
		if err == sql.ErrNoRows {
			return ac.ErrNamespaceDoesntExist
		}
		return err
	}

	var cfg aclpb.NamespaceConfig
	if err := protojson.Unmarshal([]byte(cfgStr), &cfg); err != nil {
		return err
	}

	found := false
	for i, r := range cfg.GetRelations() {
		if r.GetName() == relation.GetName() {
			found = true
			cfg.Relations[i] = relation
			break
		}
	}

	if !found {
		cfg.Relations = append(cfg.Relations, relation)
	}

	jsonConfig, err := protojson.Marshal(&cfg)
	if err != nil {
		return err
	}

	ins1 := goqu.Insert("namespace-configs").
		Cols("namespace", "config", "timestamp").
		Vals(
			goqu.Vals{cfg.Name, jsonConfig, goqu.L("NOW()")},
		)

	ins2 := goqu.Insert("namespace-changelog").
		Cols("namespace", "operation", "config", "timestamp").
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

	_, err = txn.Exec(sql1, args1...)
	if err != nil {
		return err
	}

	_, err = txn.Exec(sql2, args2...)
	if err != nil {
		return err
	}

	return txn.Commit()
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
func (i *iterator) Value() (*ac.NamespaceChangelogEntry, error) {

	var namespace, operation, configJson string
	var timestamp time.Time
	if err := i.rows.Scan(&namespace, &operation, &configJson, &timestamp); err != nil {
		return nil, err
	}

	var op ac.NamespaceOperation
	switch operation {
	case "ADD":
		op = ac.AddNamespace
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
