package tests

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	pb "catalog-service/gen/catalog/v1"
	"catalog-service/internal/auth"
	"catalog-service/internal/model"
	"catalog-service/internal/repository"
	"catalog-service/internal/service"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
)

func TestGRPCRedisDeadlineBoundsSocketWait(t *testing.T) {
	// A peer that accepts but never speaks RESP models a stalled Redis connection
	// without pausing or modifying any shared Redis server.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer lis.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := lis.Accept()
		if err != nil {
			accepted <- nil
			return
		}
		accepted <- conn
	}()
	defer func() {
		lis.Close()
		if conn := <-accepted; conn != nil {
			conn.Close()
		}
	}()
	r := redis.NewClient(&redis.Options{
		Addr: lis.Addr().String(), ContextTimeoutEnabled: true, MaxRetries: -1,
		ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second,
	})
	defer r.Close()
	c := newGRPCClient(t, service.NewCatalogService(repository.NewRedisFlavorRepository(r, "deadline:test")), 200*time.Millisecond)
	started := time.Now()
	_, err = c.GetFlavor(grpcContext(t, ""), &pb.GetFlavorRequest{FlavorId: uuid.NewString()})
	wantCode(t, err, codes.DeadlineExceeded)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Redis socket ignored request deadline: %v", elapsed)
	}
}

func TestGRPCRedisAndRESTShareLifecycle(t *testing.T) {
	repo, redisClient, prefix := redisRepositoryForTest(t)
	svc := service.NewCatalogService(repo)
	c := newGRPCClient(t, svc, time.Second)
	app := newCatalogHandlerApp(svc)
	token := handlerToken(t, auth.RoleManager)
	manager := grpcContext(t, token)
	public := grpcContext(t, "")
	in := grpcInput("gRPC Vanilla")
	in.ImageUrl = proto.String("https://example.com/vanilla.png")
	created, err := c.CreateFlavor(manager, &pb.CreateFlavorRequest{Flavor: in})
	if err != nil {
		t.Fatal(err)
	}
	f := created.Flavor.Flavor
	id := f.Id
	path := "/api/v1/flavors/" + id
	if !f.Active {
		t.Fatal("omitted active did not default to true")
	}
	if created.Flavor.Recipe != "milk; vanilla" {
		t.Fatal("manager write lost recipe")
	}
	stored, err := redisClient.Get(context.Background(), prefix+":flavor:"+id).Result()
	if err != nil {
		t.Fatal(err)
	}
	var record model.FlavorAdmin
	if err = json.Unmarshal([]byte(stored), &record); err != nil {
		t.Fatal(err)
	}
	if record.ID.String() != id || record.Price.AmountMinor != 6000 || record.Recipe != created.Flavor.Recipe {
		t.Fatal("create not persisted")
	}
	var rest model.Flavor
	if err := json.Unmarshal(catalogRequest(t, app, "GET", path, "", "", 200), &rest); err != nil {
		t.Fatal(err)
	}
	if rest.Name != f.Name || rest.Price.AmountMinor != 6000 {
		t.Fatal("REST cannot see gRPC create")
	}
	in = grpcInput("gRPC Updated")
	in.Price.AmountMinor = proto.Int64(7500)
	in.Active = proto.Bool(true)
	updated, err := c.UpdateFlavor(manager, &pb.UpdateFlavorRequest{FlavorId: id, Flavor: in})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Flavor.Flavor.ImageUrl != nil {
		t.Fatal("omitted image did not clear")
	}
	repeated, err := c.UpdateFlavor(manager, &pb.UpdateFlavorRequest{FlavorId: id, Flavor: in})
	if err != nil || !proto.Equal(updated, repeated) {
		t.Fatal("identical replacement changed state")
	}
	if err := json.Unmarshal(catalogRequest(t, app, "GET", path, "", "", 200), &rest); err != nil {
		t.Fatal(err)
	}
	if rest.Name != "gRPC Updated" || rest.Price.AmountMinor != 7500 {
		t.Fatal("REST cannot see gRPC update")
	}
	catalogRequest(t, app, "PATCH", path, `{"price":{"amount_minor":8000,"currency":"THB"}}`, token, 200)
	got, err := c.GetFlavor(public, &pb.GetFlavorRequest{FlavorId: id})
	if err != nil || got.Flavor.Price.GetAmountMinor() != 8000 {
		t.Fatalf("gRPC cannot see REST update: %v", err)
	}
	var restCreated model.FlavorAdmin
	if err := json.Unmarshal(catalogRequest(t, app, "POST", "/api/v1/flavors", replacementJSON, token, 201), &restCreated); err != nil {
		t.Fatal(err)
	}
	_, err = c.GetFlavor(public, &pb.GetFlavorRequest{FlavorId: restCreated.ID.String()})
	if err != nil {
		t.Fatal(err)
	}
	batch, err := c.BatchGetFlavors(public, &pb.BatchGetFlavorsRequest{FlavorIds: []string{restCreated.ID.String(), id}})
	if err != nil || len(batch.Items) != 2 || batch.Items[1].Id != id {
		t.Fatalf("batch read: %v %v", batch, err)
	}
	_, err = c.DeleteFlavor(manager, &pb.DeleteFlavorRequest{FlavorId: id})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.GetFlavor(public, &pb.GetFlavorRequest{FlavorId: id})
	wantCode(t, err, codes.NotFound)
	_, err = c.BatchGetFlavors(public, &pb.BatchGetFlavorsRequest{FlavorIds: []string{restCreated.ID.String(), id}})
	wantCode(t, err, codes.NotFound)
	before, err := repo.FindByID(context.Background(), uuid.MustParse(id))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.DeleteFlavor(manager, &pb.DeleteFlavorRequest{FlavorId: id})
	if err != nil {
		t.Fatal(err)
	}
	after, err := repo.FindByID(context.Background(), uuid.MustParse(id))
	if err != nil {
		t.Fatal(err)
	}
	if after.Active || !after.UpdatedAt.Equal(before.UpdatedAt) || after.Recipe == "" {
		t.Fatal("archive did not retain history or repeat changed state")
	}
	catalogRequest(t, app, "GET", path, "", "", 404)
	catalogRequest(t, app, "GET", path, "", token, 200)
	list, err := c.ListFlavors(public, &pb.ListFlavorsRequest{Active: proto.Bool(true)})
	if err != nil || len(list.Items) != 1 || list.Items[0].Id != restCreated.ID.String() {
		t.Fatalf("active list after delete: %v %v", list, err)
	}
	list, err = c.ListFlavors(manager, &pb.ListFlavorsRequest{Active: proto.Bool(false)})
	if err != nil || len(list.Items) != 1 || list.Items[0].Id != id {
		t.Fatalf("manager archive: %v %v", list, err)
	}
}
