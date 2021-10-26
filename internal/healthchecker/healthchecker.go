package healthchecker

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

type healthCheckHandler func(ctx context.Context, req *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error)

// HealthChecker implements the gRPC Health Checking Protocol.
//
// The health check behavior is injected into the HealthChecker by passing a
// healthCheckHandler.
//
// For more information about the gRPC Health Checking Protocol see:
// https://github.com/grpc/grpc/blob/master/doc/health-checking.md
type HealthChecker struct {
	grpc_health_v1.UnimplementedHealthServer
	healthCheckHandler
}

// NewHealthChecker returns a new HealthChecker instance
func NewHealthChecker(handler healthCheckHandler) *HealthChecker {
	return &HealthChecker{healthCheckHandler: handler}
}

// Check returns the server's current health status.
func (s *HealthChecker) Check(ctx context.Context, req *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	return s.healthCheckHandler(ctx, req)
}

// Watch is left unimplemented. Please use the HealthChecker.Check RPC instead.
// If this RPC is needed in the future, an implementation will be provided at
// that time.
func (s *HealthChecker) Watch(req *grpc_health_v1.HealthCheckRequest, srv grpc_health_v1.Health_WatchServer) error {
	return status.Error(codes.Unimplemented, "unimplemented")
}

// Always verify that we implement the interface
var _ grpc_health_v1.HealthServer = &HealthChecker{}
