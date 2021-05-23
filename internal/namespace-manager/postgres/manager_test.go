package postgres

import (
	"context"
	"fmt"
	"log"
	"os"
	"testing"

	"database/sql"

	aclpb "github.com/authorizer-tech/access-controller/genprotos/authorizer/accesscontroller/v1alpha1"
	ac "github.com/authorizer-tech/access-controller/internal"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/lib/pq"
	"github.com/ory/dockertest/v3"
	"github.com/ory/dockertest/v3/docker"
	"google.golang.org/protobuf/proto"
)

var (
	username = "postgres"
	password = "password"
	database = "postgres"
	port     = "5433"
	dialect  = "postgres"
)

var db *sql.DB

func TestMain(m *testing.M) {
	dockerPool, err := dockertest.NewPool("")
	if err != nil {
		log.Fatalf("Failed to connect to docker pool: %s", err)
	}

	opts := dockertest.RunOptions{
		Repository: "postgres",
		Tag:        "13",
		Env: []string{
			"POSTGRES_USER=" + username,
			"POSTGRES_PASSWORD=" + password,
			"POSTGRES_DB=" + database,
		},
		ExposedPorts: []string{"5432"},
		PortBindings: map[docker.Port][]docker.PortBinding{
			"5432": {
				{HostIP: "0.0.0.0", HostPort: port},
			},
		},
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
		log.Fatalf("Failed to establish a conn to docker Postgres database: %v", err)
	}

	driver, err := postgres.WithInstance(db, &postgres.Config{})

	migrator, err := migrate.NewWithDatabaseInstance(
		"file://../../../db/migrations",
		"postgres", driver)
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

var cfg *aclpb.NamespaceConfig = &aclpb.NamespaceConfig{
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

func TestAddConfig(t *testing.T) {

	m, err := NewNamespaceManager(db)
	if err != nil {
		log.Fatalf("Failed to instantiate the Postgres NamespaceManager: %v", err)
	}

	err = m.AddConfig(context.Background(), cfg)
	if err != nil && err != ac.ErrNamespaceAlreadyExists {
		t.Errorf("Expected nil error, but got '%v'", err)
	}

	config, err := m.GetConfig(context.Background(), cfg.Name)
	if err != nil {
		t.Errorf("Expected nil error, but got  '%v'", err)
	}

	if !proto.Equal(cfg, config) {
		t.Errorf("Expected '%v', but got '%v'", cfg, config)
	}

}

func TestGetConfig(t *testing.T) {

	m, err := NewNamespaceManager(db)
	if err != nil {
		log.Fatalf("Failed to instantiate the Postgres NamespaceManager: %v", err)
	}

	err = m.AddConfig(context.Background(), cfg)
	if err != nil && err != ac.ErrNamespaceAlreadyExists {
		t.Errorf("Expected nil error, but got '%v'", err)
	}

	config, err := m.GetConfig(context.Background(), cfg.Name)
	if err != nil {
		t.Errorf("Expected nil error, but got  '%v'", err)
	}

	if !proto.Equal(cfg, config) {
		t.Errorf("Expected '%v', but got '%v'", cfg, config)
	}
}

func TestGetRewrite(t *testing.T) {

	m, err := NewNamespaceManager(db)
	if err != nil {
		log.Fatalf("Failed to instantiate the Postgres NamespaceManager: %v", err)
	}

	err = m.AddConfig(context.Background(), cfg)
	if err != nil && err != ac.ErrNamespaceAlreadyExists {
		t.Errorf("Expected nil error, but got '%v'", err)
	}

	r1, err := m.GetRewrite(context.Background(), "namespace1", "relation2")
	if err != nil {
		t.Errorf("Expected nil error, but got  '%v'", err)
	}

	if !proto.Equal(r1, rewrite1) {
		t.Errorf("Expected '%v', but got '%v'", rewrite1, r1)
	}

	r2, err := m.GetRewrite(context.Background(), "namespace1", "relation1")
	if err != nil {
		t.Errorf("Expected nil error, but got  '%v'", err)
	}

	rewrite2 := &aclpb.Rewrite{
		RewriteOperation: &aclpb.Rewrite_Union{
			Union: &aclpb.SetOperation{
				Children: []*aclpb.SetOperation_Child{
					{ChildType: &aclpb.SetOperation_Child_This_{}},
				},
			},
		},
	}
	if !proto.Equal(r2, rewrite2) {
		t.Errorf("Expected '%v', but got '%v'", rewrite2, r2)
	}
}

func TestUpsertRelation(t *testing.T) {

	m, err := NewNamespaceManager(db)
	if err != nil {
		log.Fatalf("Failed to instantiate the Postgres NamespaceManager: %v", err)
	}

	err = m.UpsertRelation(context.Background(), "missing-namespace", &aclpb.Relation{})
	if err != ac.ErrNamespaceDoesntExist {
		t.Errorf("Expected error '%v', but got '%v'", ac.ErrNamespaceDoesntExist, err)
	}
}
