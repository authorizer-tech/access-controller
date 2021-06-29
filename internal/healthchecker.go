package accesscontroller

import (
	"context"

	"google.golang.org/grpc/health/grpc_health_v1"
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

// Watch streams back to the client a single health check status.
//
// Future implementations of this RPC could implement the health check with a
// continuous stream instead of a single status snapshot.
func (s *HealthChecker) Watch(req *grpc_health_v1.HealthCheckRequest, srv grpc_health_v1.Health_WatchServer) error {

	resp, err := s.healthCheckHandler(context.Background(), req)
	if err != nil {
		return err
	}

	return srv.Send(resp)
}
