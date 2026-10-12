package client

import (
	"context"
	"net"
	"testing"
	"time"

	paymentv1 "order-service/gen/payment/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type paymentTestServer struct {
	paymentv1.UnimplementedPaymentServiceServer
	t     *testing.T
	block bool
}

func (s *paymentTestServer) CreateCheckoutSession(ctx context.Context, req *paymentv1.CreateCheckoutSessionRequest) (*paymentv1.CreateCheckoutSessionResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	if got := md.Get("authorization"); len(got) != 1 || got[0] != "Bearer test-machine-token" {
		s.t.Error("missing or duplicate Payment machine identity")
		return nil, status.Error(codes.Unauthenticated, "service identity required")
	}
	if _, ok := ctx.Deadline(); !ok {
		s.t.Error("missing RPC deadline")
	}
	if s.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if req.OrderId != "order-test" || req.AmountMinor != 15000 || req.Currency != "THB" {
		return nil, status.Error(codes.InvalidArgument, "incorrect checkout facts")
	}
	return &paymentv1.CreateCheckoutSessionResponse{CheckoutSessionId: "cs_test", CheckoutUrl: "https://checkout.stripe.com/c/pay/test"}, nil
}

func TestPaymentClientAuthenticatedRPCAndDeadline(t *testing.T) {
	for _, block := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "timeout"}[block], func(t *testing.T) {
			listener := bufconn.Listen(1024 * 1024)
			server := grpc.NewServer()
			paymentv1.RegisterPaymentServiceServer(server, &paymentTestServer{t: t, block: block})
			go func() { _ = server.Serve(listener) }()
			t.Cleanup(func() { server.Stop(); _ = listener.Close() })
			conn, err := grpc.NewClient("passthrough:///payment-test",
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
			if err != nil {
				t.Fatal(err)
			}
			c := &paymentClient{conn: conn, inner: paymentv1.NewPaymentServiceClient(conn), token: "test-machine-token", timeout: time.Second}
			if block {
				c.timeout = 100 * time.Millisecond
			}
			t.Cleanup(func() { _ = c.Close() })
			resp, err := c.CreateCheckoutSession(context.Background(), &paymentv1.CreateCheckoutSessionRequest{OrderId: "order-test", AmountMinor: 15000, Currency: "THB"})
			if block {
				if status.Code(err) != codes.DeadlineExceeded {
					t.Fatalf("expected deadline exceeded: %v", err)
				}
			} else if err != nil || resp.CheckoutSessionId != "cs_test" {
				t.Fatalf("real gRPC round-trip failed: %v", err)
			}
		})
	}
}
