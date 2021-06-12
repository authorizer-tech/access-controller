package postgres

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"reflect"
	"testing"

	"database/sql"

	aclpb "github.com/authorizer-tech/access-controller/genprotos/authorizer/accesscontroller/v1alpha1"
	ac "github.com/authorizer-tech/access-controller/internal"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/cockroachdb"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/lib/pq"
	"github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"
	"google.golang.org/protobuf/proto"
)

var (
	username = "admin"
	password = ""
	database = "postgres"
	port     = "26258"
	dialect  = "postgres"
)

var db *sql.DB

func TestMain(m *testing.M) {

	flag.Parse()

	if testing.Short() {
		return
	}

	dockerPool, err := dockertest.NewPool("")
	if err != nil {
		log.Fatalf("Failed to connect to docker pool: %s", err)
	}

	opts := dockertest.RunOptions{
		Repository: "cockroachdb/cockroach",
		Tag:        "latest-v21.1",
		Env: []string{
			"POSTGRES_USER=" + username,
			"POSTGRES_PASSWORD=" + password,
			"POSTGRES_DB=" + database,
		},
		ExposedPorts: []string{"26258"},
		PortBindings: map[docker.Port][]docker.PortBinding{
			"26258": {
				{HostIP: "0.0.0.0", HostPort: port},
			},
		},
		Cmd: []string{"start-single-node", "--listen-addr", fmt.Sprintf(":%s", port), "--insecure", "--store=type=mem,size=2GB"},
	}

	resource, err := dockerPool.RunWithOptions(&opts, func(config *docker.HostConfig) {
		// set AutoRemove to true so that stopped container goes away by itself
		config.AutoRemove = true
		config.RestartPolicy = docker.RestartPolicy{
			Name: "no",
		}
	})
	if err != nil {
		log.Fatalf("Failed to start docker container: %s", err.Error())
	}

	dsn := fmt.Sprintf("%s://%s:%s@localhost:%s/%s?sslmode=disable", dialect, username, password, port, database)
	if err = dockerPool.Retry(func() error {
		db, err = sql.Open("postgres", dsn)
		if err != nil {
			return err
		}

		return db.Ping()
	}); err != nil {
		log.Fatalf("Failed to establish a conn to database: %v", err)
	}

	driver, err := cockroachdb.WithInstance(db, &cockroachdb.Config{})

	migrator, err := migrate.NewWithDatabaseInstance(
		"file://../../../db/migrations",
		"cockroachdb", driver)
	if err != nil {
		log.Fatalf("Failed to initialize migrator database instance: %v", err)
	}

	if err := migrator.Up(); err != nil {
		log.Fatalf("Failed to migrate up to the latest database schema: %v", err)
	}

	code := m.Run()

	if err1, err2 := migrator.Close(); err1 != nil || err2 != nil {
		log.Fatalf("Failed to close migrator source or database: %v", err)
	}

	if err := dockerPool.Purge(resource); err != nil {
		log.Fatalf("Failed to purge docker resource: %s", err)
	}

	os.Exit(code)
}

var rewrite1 *aclpb.Rewrite = &aclpb.Rewrite{
	RewriteOperation: &aclpb.Rewrite_Union{
		Union: &aclpb.SetOperation{
			Children: []*aclpb.SetOperation_Child{
				{
					ChildType: &aclpb.SetOperation_Child_ComputedSubjectset{
						ComputedSubjectset: &aclpb.ComputedSubjectset{
							Relation: "relation3",
						},
					},
				},
			},
		},
	},
}

var cfg1 *aclpb.NamespaceConfig = &aclpb.NamespaceConfig{
	Name: "namespace1",
	Relations: []*aclpb.Relation{
		{
			Name: "relation1",
		},
		{
			Name:    "relation2",
			Rewrite: rewrite1,
		},
	},
}

var cfg2 *aclpb.NamespaceConfig = &aclpb.NamespaceConfig{
	Name: cfg1.Name,
	Relations: []*aclpb.Relation{
		{Name: "relationX"},
	},
}

var defaultRewrite *aclpb.Rewrite = &aclpb.Rewrite{
	RewriteOperation: &aclpb.Rewrite_Union{
		Union: &aclpb.SetOperation{
			Children: []*aclpb.SetOperation_Child{
				{ChildType: &aclpb.SetOperation_Child_This_{}},
			},
		},
	},
}

func TestNamespaceManager(t *testing.T) {

	m, err := NewNamespaceManager(db)
	if err != nil {
		log.Fatalf("Failed to instantiate the Postgres NamespaceManager: %v", err)
	}

	// Add a new namespace configuration, and verify it by reading it
	// back
	err = m.UpsertConfig(context.Background(), cfg1)
	if err != nil {
		t.Errorf("Expected nil error, but got '%v'", err)
	}

	config, err := m.GetConfig(context.Background(), cfg1.Name)
	if err != nil {
		t.Errorf("Expected nil error, but got  '%v'", err)
	}
	if !proto.Equal(cfg1, config) {
		t.Errorf("Expected '%v', but got '%v'", cfg1, config)
	}

	// Attempt to get a namespace config for a namespace that doesn't exist, verify
	// it's nil
	config, err = m.GetConfig(context.Background(), "missing-namespace")
	if err != ac.ErrNamespaceDoesntExist {
		t.Errorf("Expected error '%v', but got '%v'", ac.ErrNamespaceDoesntExist, err)
	}
	if config != nil {
		t.Errorf("Expected nil config, but got '%v", config)
	}

	// Verify the rewrite rule for 'relation2'
	r1, err := m.GetRewrite(context.Background(), cfg1.Name, "relation2")
	if err != nil {
		t.Errorf("Expected nil error, but got  '%v'", err)
	}
	if !proto.Equal(r1, rewrite1) {
		t.Errorf("Expected '%v', but got '%v'", rewrite1, r1)
	}

	// Verify the rewrite rule for 'relation1'
	r2, err := m.GetRewrite(context.Background(), cfg1.Name, "relation1")
	if err != nil {
		t.Errorf("Expected nil error, but got  '%v'", err)
	}

	if !proto.Equal(r2, defaultRewrite) {
		t.Errorf("Expected '%v', but got '%v'", defaultRewrite, r2)
	}

	// Overwrite the namespace config, and verify it by reading it back
	err = m.UpsertConfig(context.Background(), cfg2)
	if err != nil {
		t.Errorf("Expected nil error, but got '%v'", err)
	}

	config, err = m.GetConfig(context.Background(), cfg2.Name)
	if err != nil {
		t.Errorf("Expected nil error, but got  '%v'", err)
	}
	if !proto.Equal(cfg2, config) {
		t.Errorf("Expected '%v', but got '%v'", cfg1, config)
	}

	rewrite, err := m.GetRewrite(context.Background(), cfg2.Name, "relationX")
	if err != nil {
		t.Errorf("Expected nil error, but got '%v'", err)
	}
	if !proto.Equal(rewrite, defaultRewrite) {
		t.Errorf("Expected rewrite '%v', but got '%v'", defaultRewrite, rewrite)
	}

	// Fetch (at most) the top 4 most recent namespace config changelog entries
	iter, err := m.TopChanges(context.Background(), 4)
	if err != nil {
		t.Errorf("Expected error to be nil, but got '%v'", err)
	}

	changelog := []*ac.NamespaceChangelogEntry{}
	for iter.Next() {

		entry, err := iter.Value()
		if err != nil {
			t.Fatalf("Expected nil error, but got '%v'", err)
		}

		changelog = append(changelog, entry)

	}
	if err := iter.Close(context.Background()); err != nil {
		t.Fatalf("Failed to close the Changelog iterator: %v", err)
	}

	if len(changelog) != 2 {
		t.Errorf("Expected 2 changelog entries, but got '%d'", len(changelog))
	}

	// changelog entries are sorted in timestamp acending order (least recent first)
	expected := []*ac.NamespaceChangelogEntry{
		{
			Namespace: cfg1.Name,
			Operation: ac.AddNamespace,
			Config:    changelog[0].Config, // todo: assert the correct value here too
			Timestamp: changelog[0].Timestamp,
		},
		{
			Namespace: cfg2.Name,
			Operation: ac.UpdateNamespace,
			Config:    changelog[1].Config, // todo: assert the correct value here too
			Timestamp: changelog[1].Timestamp,
		},
	}

	if !reflect.DeepEqual(expected, changelog) {
		t.Errorf("Changelogs were different")
	}
}
