package cataloggrpc

import (
	"context"
	"strings"
	"time"

	pb "catalog-service/gen/catalog/v1"
	"catalog-service/internal/auth"
	"catalog-service/internal/service"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
)

type identityKey struct{}

func interceptor(verifier *auth.Verifier, timeout time.Duration) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		md, _ := metadata.FromIncomingContext(ctx)
		values := md.Get("authorization")
		if len(values) > 0 {
			parts := strings.Fields(values[0])
			if len(values) != 1 || len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
				return nil, unauthenticated()
			}
			claims, err := verifier.Parse(parts[1])
			if err != nil {
				return nil, unauthenticated()
			}
			ctx = context.WithValue(ctx, identityKey{}, claims)
		}
		switch info.FullMethod {
		case pb.CatalogService_GetFlavor_FullMethodName,
			pb.CatalogService_ListFlavors_FullMethodName,
			pb.CatalogService_BatchGetFlavors_FullMethodName:
			// Anonymous reads use PublicRead. List(active=false) checks Manager below.
		default:
			if err := requireManager(ctx); err != nil {
				return nil, err
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, serviceError(err)
		}
		response, err := next(ctx, req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, serviceError(ctx.Err())
			}
			// A socket deadline can fire just before the context's timer does.
			if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
				return nil, serviceError(context.DeadlineExceeded)
			}
		}
		return response, err
	}
}

func requireManager(ctx context.Context) error {
	claims, _ := ctx.Value(identityKey{}).(*auth.Claims)
	if claims == nil {
		return unauthenticated()
	}
	if claims.Role != auth.RoleManager {
		return rpcError(codes.PermissionDenied, "FORBIDDEN", "Manager role is required")
	}
	return nil
}

func readScope(ctx context.Context) service.ReadScope {
	claims, _ := ctx.Value(identityKey{}).(*auth.Claims)
	if claims != nil && claims.Role == auth.RoleManager {
		return service.ManagerRead
	}
	return service.PublicRead
}

func unauthenticated() error {
	return rpcError(codes.Unauthenticated, "UNAUTHORIZED", "Authentication is required")
}
