package client

import (
	"context"
	"errors"
	"time"

	paymentv1 "order-service/gen/payment/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

type Payment interface {
	CreateCheckoutSession(context.Context, *paymentv1.CreateCheckoutSessionRequest) (*paymentv1.CreateCheckoutSessionResponse, error)
}

type paymentClient struct {
	conn    *grpc.ClientConn
	inner   paymentv1.PaymentServiceClient
	token   string
	timeout time.Duration
}

func NewPaymentClient(address, token string, timeout time.Duration) (*paymentClient, error) {
	if address == "" || token == "" || timeout <= 0 {
		return nil, errors.New("payment address, service token and positive timeout are required")
	}
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	return &paymentClient{conn: conn, inner: paymentv1.NewPaymentServiceClient(conn), token: token, timeout: timeout}, nil
}

func (c *paymentClient) CreateCheckoutSession(ctx context.Context, req *paymentv1.CreateCheckoutSessionRequest) (*paymentv1.CreateCheckoutSessionResponse, error) {
	callCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	callCtx = metadata.AppendToOutgoingContext(callCtx, "authorization", "Bearer "+c.token)
	// No automatic mutation retries: a timeout can still have created a session.
	// The next checkout request uses the same durable order and checkout key.
	return c.inner.CreateCheckoutSession(callCtx, req)
}

func (c *paymentClient) Close() error { return c.conn.Close() }
