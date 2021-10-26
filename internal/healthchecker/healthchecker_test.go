package healthchecker

import (
	"context"
	"fmt"
	"testing"

	"github.com/pkg/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestHealthChecker_Check(t *testing.T) {

	handlerErr := fmt.Errorf("some error")

	type input struct {
		handler healthCheckHandler
		ctx     context.Context
		request *grpc_health_v1.HealthCheckRequest
	}

	type output struct {
		response *grpc_health_v1.HealthCheckResponse
		err      error
	}

	tests := []struct {
		name string
		input
		output
	}{
		{
			name: "Test-1",
			input: input{
				handler: func(ctx context.Context, req *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
					return nil, handlerErr
				},
			},
			output: output{
				err: handlerErr,
			},
		},
		{
			name: "Test-2",
			input: input{
				handler: func(ctx context.Context, req *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
					return nil, nil
				},
			},
			output: output{},
		},
		{
			name: "Test-3",
			input: input{
				handler: func(ctx context.Context, req *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
					return &grpc_health_v1.HealthCheckResponse{
						Status: grpc_health_v1.HealthCheckResponse_SERVING,
					}, nil
				},
			},
			output: output{
				response: &grpc_health_v1.HealthCheckResponse{
					Status: grpc_health_v1.HealthCheckResponse_SERVING,
				},
			},
		},
		{
			name: "Test-4",
			input: input{
				handler: func(ctx context.Context, req *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
					return &grpc_health_v1.HealthCheckResponse{
						Status: grpc_health_v1.HealthCheckResponse_NOT_SERVING,
					}, nil
				},
			},
			output: output{
				response: &grpc_health_v1.HealthCheckResponse{
					Status: grpc_health_v1.HealthCheckResponse_NOT_SERVING,
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			checker := NewHealthChecker(test.input.handler)

			ctx := test.input.ctx
			if ctx == nil {
				ctx = context.Background()
			}

			response, err := checker.Check(ctx, test.input.request)

			if !errors.Is(err, test.output.err) {
				t.Errorf("Expected error '%v', but got '%v'", test.output.err, err)
			}

			if !proto.Equal(response, test.output.response) {
				t.Errorf("Expected response '%v', but got '%v'", test.output.response, response)
			}
		})
	}
}

func TestHealthCheck_Watch(t *testing.T) {

	checker := NewHealthChecker(nil)

	expected := status.Error(codes.Unimplemented, "unimplemented")

	err := checker.Watch(nil, nil)
	if !errors.Is(err, expected) {
		t.Errorf("Expected error '%v', but got '%v'", expected, err)
	}
}
