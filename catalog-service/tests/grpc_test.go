package tests

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	pb "catalog-service/gen/catalog/v1"
	"catalog-service/internal/auth"
	"catalog-service/internal/model"
	"catalog-service/internal/repository"
	"catalog-service/internal/service"
	cataloggrpc "catalog-service/internal/transport/grpc"

	"github.com/google/uuid"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// The generated client uses TCP and protobuf serialization in every transport test.
func newGRPCClient(t *testing.T, svc service.CatalogService, timeout time.Duration) pb.CatalogServiceClient {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := cataloggrpc.NewServer(svc, auth.NewVerifier(handlerTestSecret, "gelatoflow-auth", "gelatoflow-api"), timeout)
	done := make(chan error, 1)
	go func() { done <- s.Serve(lis) }()
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		s.Stop()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		s.Stop()
		if err := <-done; err != nil {
			t.Errorf("serve gRPC: %v", err)
		}
	})
	return pb.NewCatalogServiceClient(conn)
}

func grpcContext(t *testing.T, token string) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	if token != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
	}
	return ctx
}

func grpcInput(name string) *pb.FlavorInput {
	return &pb.FlavorInput{
		Name: proto.String(name), Description: proto.String("Vanilla gelato"),
		Price:     &pb.Money{AmountMinor: proto.Int64(6000), Currency: proto.String("THB")},
		Allergens: &pb.AllergenList{Values: []pb.Allergen{pb.Allergen_ALLERGEN_MILK}},
		Recipe:    proto.String("milk; vanilla"),
	}
}

func wantCode(t *testing.T, err error, code codes.Code) {
	t.Helper()
	if status.Code(err) != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}

type lockedRepository struct {
	mu    sync.Mutex
	inner *memoryFlavorRepository
}

func newLockedRepository() *lockedRepository {
	return &lockedRepository{inner: newMemoryFlavorRepository()}
}
func (r *lockedRepository) Create(ctx context.Context, f *model.FlavorAdmin) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.inner.Create(ctx, f)
}
func (r *lockedRepository) FindByID(ctx context.Context, id uuid.UUID) (*model.FlavorAdmin, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.inner.FindByID(ctx, id)
}
func (r *lockedRepository) List(ctx context.Context) ([]*model.FlavorAdmin, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.inner.List(ctx)
}
func (r *lockedRepository) Update(ctx context.Context, prev, next *model.FlavorAdmin) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.inner.Update(ctx, prev, next)
}

func TestGRPCAuthAndArchiveVisibility(t *testing.T) {
	repo := newLockedRepository()
	c := newGRPCClient(t, service.NewCatalogService(repo), time.Second)
	managerToken := handlerToken(t, auth.RoleManager)
	manager := grpcContext(t, managerToken)
	created, err := c.CreateFlavor(manager, &pb.CreateFlavorRequest{Flavor: grpcInput("Archive")})
	if err != nil {
		t.Fatal(err)
	}
	id := created.Flavor.Flavor.Id
	if _, err := c.DeleteFlavor(manager, &pb.DeleteFlavorRequest{FlavorId: id}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, token string
		writeCode   codes.Code
	}{
		{"anonymous", "", codes.Unauthenticated},
		{"customer", handlerToken(t, auth.RoleCustomer), codes.PermissionDenied},
		{"staff", handlerToken(t, auth.RoleStaff), codes.PermissionDenied},
		{"bad token", "invalid", codes.Unauthenticated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := grpcContext(t, tc.token)
			_, err := c.CreateFlavor(ctx, &pb.CreateFlavorRequest{Flavor: grpcInput("Denied")})
			wantCode(t, err, tc.writeCode)
			_, err = c.UpdateFlavor(ctx, &pb.UpdateFlavorRequest{FlavorId: id, Flavor: grpcInput("Denied")})
			wantCode(t, err, tc.writeCode)
			_, err = c.DeleteFlavor(ctx, &pb.DeleteFlavorRequest{FlavorId: id})
			wantCode(t, err, tc.writeCode)
			readCode := codes.NotFound
			if tc.name == "bad token" {
				readCode = codes.Unauthenticated
			}
			_, err = c.GetFlavor(ctx, &pb.GetFlavorRequest{FlavorId: id})
			wantCode(t, err, readCode)
			_, err = c.BatchGetFlavors(ctx, &pb.BatchGetFlavorsRequest{FlavorIds: []string{id}})
			wantCode(t, err, readCode)
			_, err = c.ListFlavors(ctx, &pb.ListFlavorsRequest{Active: proto.Bool(false)})
			wantCode(t, err, tc.writeCode)
			list, err := c.ListFlavors(ctx, &pb.ListFlavorsRequest{})
			if tc.name == "bad token" {
				wantCode(t, err, codes.Unauthenticated)
			} else if err != nil || len(list.Items) != 0 {
				t.Fatalf("archive exposed: %v %v", list, err)
			}
		})
	}
	for _, values := range [][]string{{""}, {"Basic token"}, {"Bearer " + managerToken, "Bearer " + managerToken}, {"Bearer " + managerToken + ", Bearer " + managerToken}} {
		ctx := metadata.NewOutgoingContext(grpcContext(t, ""), metadata.MD{"authorization": values})
		_, err := c.ListFlavors(ctx, &pb.ListFlavorsRequest{})
		wantCode(t, err, codes.Unauthenticated)
	}
	spoof := metadata.AppendToOutgoingContext(grpcContext(t, ""), "x-user-role", "MANAGER", "x-user-id", uuid.NewString())
	_, err = c.DeleteFlavor(spoof, &pb.DeleteFlavorRequest{FlavorId: id})
	wantCode(t, err, codes.Unauthenticated)
	got, err := c.GetFlavor(manager, &pb.GetFlavorRequest{FlavorId: id})
	if err != nil || got.Flavor.Active {
		t.Fatalf("manager cannot inspect archive: %v %v", got, err)
	}
	if got.Flavor.ProtoReflect().Descriptor().Fields().ByName("recipe") != nil {
		t.Fatal("read schema exposes recipe")
	}
	list, err := c.ListFlavors(manager, &pb.ListFlavorsRequest{Active: proto.Bool(false)})
	if err != nil || len(list.Items) != 1 {
		t.Fatalf("manager archive list: %v %v", list, err)
	}
}

func TestGRPCFieldPresenceAndValidation(t *testing.T) {
	c := newGRPCClient(t, service.NewCatalogService(newLockedRepository()), time.Second)
	ctx := grpcContext(t, handlerToken(t, auth.RoleManager))
	for name, mutate := range map[string]func(*pb.FlavorInput){
		"missing name":         func(v *pb.FlavorInput) { v.Name = nil },
		"missing description":  func(v *pb.FlavorInput) { v.Description = nil },
		"missing recipe":       func(v *pb.FlavorInput) { v.Recipe = nil },
		"missing price":        func(v *pb.FlavorInput) { v.Price = nil },
		"missing amount":       func(v *pb.FlavorInput) { v.Price.AmountMinor = nil },
		"missing currency":     func(v *pb.FlavorInput) { v.Price.Currency = nil },
		"negative price":       func(v *pb.FlavorInput) { v.Price.AmountMinor = proto.Int64(-1) },
		"wrong currency":       func(v *pb.FlavorInput) { v.Price.Currency = proto.String("USD") },
		"missing allergens":    func(v *pb.FlavorInput) { v.Allergens = nil },
		"unspecified allergen": func(v *pb.FlavorInput) { v.Allergens.Values = []pb.Allergen{0} },
		"unknown allergen":     func(v *pb.FlavorInput) { v.Allergens.Values = []pb.Allergen{99} },
		"duplicate allergen":   func(v *pb.FlavorInput) { v.Allergens.Values = []pb.Allergen{1, 1} },
		"invalid image":        func(v *pb.FlavorInput) { v.ImageUrl = proto.String("file:///private") },
	} {
		t.Run(name, func(t *testing.T) {
			v := grpcInput(name)
			mutate(v)
			_, err := c.CreateFlavor(ctx, &pb.CreateFlavorRequest{Flavor: v})
			wantCode(t, err, codes.InvalidArgument)
		})
	}
	_, err := c.CreateFlavor(ctx, &pb.CreateFlavorRequest{})
	wantCode(t, err, codes.InvalidArgument)
	v := grpcInput("Zero")
	v.Description = proto.String("")
	v.Price.AmountMinor = proto.Int64(0)
	v.Allergens = &pb.AllergenList{}
	v.Active = proto.Bool(false)
	created, err := c.CreateFlavor(ctx, &pb.CreateFlavorRequest{Flavor: v})
	if err != nil {
		t.Fatal(err)
	}
	if created.Flavor.Flavor.Active || created.Flavor.Flavor.Price.GetAmountMinor() != 0 || created.Flavor.Flavor.Description != "" {
		t.Fatal("explicit zero values lost")
	}
	id := created.Flavor.Flavor.Id
	for name, mutate := range map[string]func(*pb.FlavorInput){
		"active":       func(v *pb.FlavorInput) { v.Active = nil },
		"description":  func(v *pb.FlavorInput) { v.Description = nil },
		"price amount": func(v *pb.FlavorInput) { v.Price.AmountMinor = nil },
		"allergens":    func(v *pb.FlavorInput) { v.Allergens = nil },
	} {
		t.Run("replace missing "+name, func(t *testing.T) {
			in := proto.Clone(v).(*pb.FlavorInput)
			mutate(in)
			_, err := c.UpdateFlavor(ctx, &pb.UpdateFlavorRequest{FlavorId: id, Flavor: in})
			wantCode(t, err, codes.InvalidArgument)
		})
	}
	updated, err := c.UpdateFlavor(ctx, &pb.UpdateFlavorRequest{FlavorId: id, Flavor: v})
	if err != nil || !proto.Equal(created.Flavor, updated.Flavor) {
		t.Fatalf("identical replace changed record: %v", err)
	}
	_, err = c.CreateFlavor(ctx, &pb.CreateFlavorRequest{Flavor: v})
	wantCode(t, err, codes.AlreadyExists)
	_, err = c.GetFlavor(ctx, &pb.GetFlavorRequest{FlavorId: "bad"})
	wantCode(t, err, codes.InvalidArgument)
	_, err = c.UpdateFlavor(ctx, &pb.UpdateFlavorRequest{FlavorId: "bad", Flavor: v})
	wantCode(t, err, codes.InvalidArgument)
	_, err = c.DeleteFlavor(ctx, &pb.DeleteFlavorRequest{FlavorId: uuid.NewString()})
	wantCode(t, err, codes.NotFound)
	list, err := c.ListFlavors(ctx, &pb.ListFlavorsRequest{})
	if err != nil || len(list.Items) != 1 {
		t.Fatal("rejected writes changed records")
	}
}

func TestGRPCBatchGetContract(t *testing.T) {
	c := newGRPCClient(t, service.NewCatalogService(newLockedRepository()), time.Second)
	manager := grpcContext(t, handlerToken(t, auth.RoleManager))
	public := grpcContext(t, "")
	first, err := c.CreateFlavor(manager, &pb.CreateFlavorRequest{Flavor: grpcInput("A")})
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.CreateFlavor(manager, &pb.CreateFlavorRequest{Flavor: grpcInput("B")})
	if err != nil {
		t.Fatal(err)
	}
	a, b := first.Flavor.Flavor.Id, second.Flavor.Flavor.Id
	result, err := c.BatchGetFlavors(public, &pb.BatchGetFlavorsRequest{FlavorIds: []string{b, a}})
	if err != nil || len(result.Items) != 2 || result.Items[0].Id != b || result.Items[1].Id != a {
		t.Fatalf("order lost: %v %v", result, err)
	}
	for _, ids := range [][]string{nil, {a, a}, {"invalid"}, make([]string, 101)} {
		_, err := c.BatchGetFlavors(public, &pb.BatchGetFlavorsRequest{FlavorIds: ids})
		wantCode(t, err, codes.InvalidArgument)
	}
	result, err = c.BatchGetFlavors(public, &pb.BatchGetFlavorsRequest{FlavorIds: []string{a, uuid.NewString()}})
	wantCode(t, err, codes.NotFound)
	if result != nil {
		t.Fatal("partial result returned")
	}
}

type failureRepository struct {
	service.FlavorRepository
	findErr   error
	updateErr error
}

func (r *failureRepository) FindByID(ctx context.Context, id uuid.UUID) (*model.FlavorAdmin, error) {
	if r.findErr != nil {
		return nil, r.findErr
	}
	return r.FlavorRepository.FindByID(ctx, id)
}
func (r *failureRepository) Update(ctx context.Context, prev, next *model.FlavorAdmin) error {
	if r.updateErr != nil {
		return r.updateErr
	}
	return r.FlavorRepository.Update(ctx, prev, next)
}

func TestGRPCErrorMappingAndConflicts(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code codes.Code
	}{
		{fmt.Errorf("wrapped: %w", context.Canceled), codes.Canceled},
		{fmt.Errorf("wrapped: %w", context.DeadlineExceeded), codes.DeadlineExceeded},
		{&net.OpError{Op: "dial", Net: "tcp", Err: errors.New("private redis URL")}, codes.Unavailable},
		{errors.New("private stored recipe and credentials"), codes.Internal},
	} {
		t.Run(tc.code.String(), func(t *testing.T) {
			c := newGRPCClient(t, service.NewCatalogService(&failureRepository{findErr: tc.err}), time.Second)
			_, err := c.GetFlavor(grpcContext(t, ""), &pb.GetFlavorRequest{FlavorId: uuid.NewString()})
			wantCode(t, err, tc.code)
			if strings.Contains(err.Error(), "private") {
				t.Fatal("internal error leaked")
			}
		})
	}
	repo := &failureRepository{FlavorRepository: newLockedRepository(), updateErr: repository.ErrUpdateConflict}
	c := newGRPCClient(t, service.NewCatalogService(repo), time.Second)
	ctx := grpcContext(t, handlerToken(t, auth.RoleManager))
	created, err := c.CreateFlavor(ctx, &pb.CreateFlavorRequest{Flavor: grpcInput("Conflict")})
	if err != nil {
		t.Fatal(err)
	}
	in := grpcInput("Changed")
	in.Active = proto.Bool(true)
	_, err = c.UpdateFlavor(ctx, &pb.UpdateFlavorRequest{FlavorId: created.Flavor.Flavor.Id, Flavor: in})
	wantCode(t, err, codes.Aborted)
	details := status.Convert(err).Details()
	if len(details) != 1 || details[0].(*errdetails.ErrorInfo).Reason != "FLAVOR_UPDATE_CONFLICT" {
		t.Fatalf("missing stable reason: %v", details)
	}
	_, err = c.DeleteFlavor(ctx, &pb.DeleteFlavorRequest{FlavorId: created.Flavor.Flavor.Id})
	wantCode(t, err, codes.Aborted)
}

type waitingRepository struct {
	service.FlavorRepository
	started  chan struct{}
	finished chan error
}

func (r *waitingRepository) FindByID(ctx context.Context, _ uuid.UUID) (*model.FlavorAdmin, error) {
	close(r.started)
	<-ctx.Done()
	r.finished <- ctx.Err()
	return nil, ctx.Err()
}

func TestGRPCDeadlinesAndCancellationReachRepository(t *testing.T) {
	for _, mode := range []string{"server deadline", "client deadline", "client cancel"} {
		t.Run(mode, func(t *testing.T) {
			r := &waitingRepository{started: make(chan struct{}), finished: make(chan error, 1)}
			timeout := 2 * time.Second
			if mode == "server deadline" {
				timeout = 50 * time.Millisecond
			}
			c := newGRPCClient(t, service.NewCatalogService(r), timeout)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if mode == "client deadline" {
				var shortCancel context.CancelFunc
				ctx, shortCancel = context.WithTimeout(ctx, 100*time.Millisecond)
				defer shortCancel()
			}
			done := make(chan error, 1)
			go func() { _, err := c.GetFlavor(ctx, &pb.GetFlavorRequest{FlavorId: uuid.NewString()}); done <- err }()
			select {
			case <-r.started:
			case <-time.After(time.Second):
				t.Fatal("RPC did not start")
			}
			code := codes.DeadlineExceeded
			if mode == "client cancel" {
				cancel()
				code = codes.Canceled
			}
			wantCode(t, <-done, code)
			select {
			case err := <-r.finished:
				if err == nil {
					t.Fatal("missing cancellation")
				}
			case <-time.After(time.Second):
				t.Fatal("repository did not receive cancellation")
			}
		})
	}
}

func TestGRPCRejectsOversizedRequest(t *testing.T) {
	c := newGRPCClient(t, service.NewCatalogService(newLockedRepository()), time.Second)
	in := grpcInput("Too large")
	in.Recipe = proto.String(strings.Repeat("x", 1<<20))
	_, err := c.CreateFlavor(grpcContext(t, handlerToken(t, auth.RoleManager)), &pb.CreateFlavorRequest{Flavor: in})
	wantCode(t, err, codes.ResourceExhausted)
}
