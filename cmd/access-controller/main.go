package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/google/uuid"
	gwruntime "github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	_ "github.com/lib/pq"
	"github.com/spf13/viper"

	aclpb "github.com/authorizer-tech/access-controller/genprotos/authorizer/accesscontroller/v1alpha1"
	ac "github.com/authorizer-tech/access-controller/internal"
	"github.com/authorizer-tech/access-controller/internal/datastores"
	"github.com/authorizer-tech/access-controller/internal/namespace-manager/postgres"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

var serverID = flag.String("id", uuid.New().String(), "A unique identifier for the server. Defaults to a new uuid.")
var nodePort = flag.Int("node-port", 7946, "The bind port for the cluster node")
var advertise = flag.String("advertise", "", "The address that this node advertises on within the cluster")
var grpcPort = flag.Int("grpc-port", 50052, "The bind port for the grpc server")
var httpPort = flag.Int("http-port", 8082, "The bind port for the grpc-gateway http server")
var join = flag.String("join", "", "A comma-separated list of 'host:port' addresses for nodes in the cluster to join to")
var insecure = flag.Bool("insecure", false, "Run in insecure mode (no tls)")
var namespaceConfigPath = flag.String("namespace-config", "./testdata/namespace-configs", "The path to the namespace configurations")
var configPath = flag.String("config", "./localconfig/config.yaml", "The path to the server config")

type Config struct {
	GrpcGateway struct {
		Enabled bool
	}

	Postgres struct {
		Host     string
		Port     int
		Database string
	}
}

func main() {

	flag.Parse()

	viper.SetConfigFile(*configPath)
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("Failed to load server config file: %v", err)
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		log.Fatalf("Failed to Unmarshal server config: %v", err)
	}

	pgUsername := viper.GetString("POSTGRES_USERNAME")
	pgPassword := viper.GetString("POSTGRES_PASSWORD")
	dsn := fmt.Sprintf("postgresql://%s:%s@%s:%d/%s?sslmode=disable",
		pgUsername,
		pgPassword,
		cfg.Postgres.Host,
		cfg.Postgres.Port,
		cfg.Postgres.Database,
	)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("Failed to establish a connection to Postgres database: %v", err)
	}

	datastore := &datastores.SQLStore{
		DB: db,
	}

	m, err := postgres.NewNamespaceManager(db)
	if err != nil {
		log.Fatalf("Failed to initialize postgres NamespaceManager: %v", err)
	}

	log.Info("Starting access-controller")
	log.Infof("  Version: %s", version)
	log.Infof("  Date: %s", date)
	log.Infof("  Commit: %s", commit)
	log.Infof("  Go version: %s", runtime.Version())

	ctrlOpts := []ac.AccessControllerOption{
		ac.WithStore(datastore),
		ac.WithNamespaceManager(m),
		ac.WithNodeConfigs(ac.NodeConfigs{
			ServerID:   *serverID,
			Advertise:  *advertise,
			Join:       *join,
			NodePort:   *nodePort,
			ServerPort: *grpcPort,
		}),
	}
	controller, err := ac.NewAccessController(ctrlOpts...)
	if err != nil {
		log.Fatalf("Failed to initialize the access-controller: %v", err)
	}

	addr := fmt.Sprintf(":%d", *grpcPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("Failed to start the TCP listener on '%v': %v", addr, err)
	}

	grpcOpts := []grpc.ServerOption{}
	server := grpc.NewServer(grpcOpts...)
	aclpb.RegisterCheckServiceServer(server, controller)
	aclpb.RegisterWriteServiceServer(server, controller)
	aclpb.RegisterReadServiceServer(server, controller)
	aclpb.RegisterExpandServiceServer(server, controller)
	aclpb.RegisterNamespaceConfigServiceServer(server, controller)

	go func() {
		reflection.Register(server)

		log.Infof("Starting grpc server at '%v'..", addr)

		if err := server.Serve(listener); err != nil {
			log.Fatalf("Failed to start the gRPC server: %v", err)
		}
	}()

	var gateway *http.Server
	if cfg.GrpcGateway.Enabled {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Register gRPC server endpoint
		// Note: Make sure the gRPC server is running properly and accessible
		mux := gwruntime.NewServeMux()

		opts := []grpc.DialOption{grpc.WithInsecure()}

		if err := aclpb.RegisterCheckServiceHandlerFromEndpoint(ctx, mux, addr, opts); err != nil {
			log.Fatalf("Failed to initialize grpc-gateway CheckService handler: %v", err)
		}

		if err := aclpb.RegisterWriteServiceHandlerFromEndpoint(ctx, mux, addr, opts); err != nil {
			log.Fatalf("Failed to initialize grpc-gateway WriteService handler: %v", err)
		}

		if err := aclpb.RegisterReadServiceHandlerFromEndpoint(ctx, mux, addr, opts); err != nil {
			log.Fatalf("Failed to initialize grpc-gateway ReadService handler: %v", err)
		}

		if err := aclpb.RegisterNamespaceConfigServiceHandlerFromEndpoint(ctx, mux, addr, opts); err != nil {
			log.Fatalf("Failed to initialize grpc-gateway NamespaceConfig handler: %v", err)
		}

		gateway = &http.Server{
			Addr:    fmt.Sprintf(":%d", *httpPort),
			Handler: mux,
		}

		go func() {
			log.Infof("Starting grpc-gateway server at '%v'..", gateway.Addr)

			// Start HTTP server (and proxy calls to gRPC server endpoint)
			if err := gateway.ListenAndServe(); err != http.ErrServerClosed {
				log.Fatalf("Failed to start grpc-gateway HTTP server: %v", err)
			}
		}()
	}

	exit := make(chan os.Signal, 1)
	signal.Notify(exit, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	<-exit

	log.Info("Shutting Down..")

	if cfg.GrpcGateway.Enabled {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		if err := gateway.Shutdown(ctx); err != nil {
			log.Errorf("Failed to gracefully shutdown the grpc-gateway server: %v", err)
		}
	}

	server.Stop()
	if err := controller.Close(); err != nil {
		log.Errorf("Failed to gracefully close the access-controller: %v", err)
	}

	log.Info("Shutdown Complete. Goodbye 👋")
}
