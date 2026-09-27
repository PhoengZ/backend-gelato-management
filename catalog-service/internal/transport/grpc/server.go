package cataloggrpc

import (
	"time"

	pb "catalog-service/gen/catalog/v1"
	"catalog-service/internal/auth"
	"catalog-service/internal/service"

	"google.golang.org/grpc"
)

// NewServer registers the protobuf transport. Callers own its listener/lifecycle.
// Plaintext is for loopback or isolated local development only; deployment must
// supply transport security at the network boundary before exposing it remotely.
func NewServer(svc service.CatalogService, verifier *auth.Verifier, timeout time.Duration) *grpc.Server {
	server := grpc.NewServer(
		grpc.UnaryInterceptor(interceptor(verifier, timeout)),
		grpc.MaxRecvMsgSize(1<<20),
		grpc.MaxSendMsgSize(4<<20),
		grpc.MaxHeaderListSize(16<<10),
	)
	pb.RegisterCatalogServiceServer(server, &handler{svc: svc})
	return server
}
