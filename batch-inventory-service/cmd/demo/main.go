// Demo creates uniquely named Catalog data and new Inventory batches only.
// It does not authenticate through Auth or represent an implemented Order client.
package main

import (
	pb "batch-inventory-service/gen/inventory/v1"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"io"
	"net/http"
	"os"
	"time"
)

func env(k, d string) string {
	if s := os.Getenv(k); s != "" {
		return s
	}
	return d
}
func request(ctx context.Context, method, url, token string, in, out any, expected int) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode != expected {
		return fmt.Errorf("%s: %d %s", method, res.StatusCode, raw)
	}
	if out != nil {
		return json.Unmarshal(raw, out)
	}
	return nil
}
func run() error {
	token := os.Getenv("MANAGER_TOKEN")
	orderToken := os.Getenv("ORDER_SERVICE_TOKEN")
	if token == "" || orderToken == "" {
		return fmt.Errorf("set MANAGER_TOKEN and ORDER_SERVICE_TOKEN")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	catalog := env("CATALOG_REST_URL", "http://127.0.0.1:3002")
	rest := env("INVENTORY_REST_URL", "http://127.0.0.1:3004")
	var flavor struct {
		ID string `json:"id"`
	}
	if err := request(ctx, "POST", catalog+"/api/v1/flavors", token, map[string]any{"name": "Inventory Demo " + uuid.NewString(), "description": "FEFO demo", "price": map[string]any{"amount_minor": 6000, "currency": "THB"}, "allergens": []string{}, "recipe": "demo"}, &flavor, 201); err != nil {
		return err
	}
	fmt.Println("Catalog REST: created unique active flavor", flavor.ID)
	defer func() {
		if err := request(ctx, "DELETE", catalog+"/api/v1/flavors/"+flavor.ID, token, nil, nil, 204); err != nil {
			fmt.Fprintln(os.Stderr, "Archive demo flavor:", err)
		}
	}()
	ids := []string{}
	for i, n := range []int{2, 4} {
		var batch struct {
			ID string `json:"id"`
		}
		if err := request(ctx, "POST", rest+"/api/v1/inventory/batches", token, map[string]any{"flavor_id": flavor.ID, "production_date": time.Now().UTC().Format("2006-01-02"), "expires_at": time.Now().Add(time.Duration(i+1) * time.Hour).UTC().Format(time.RFC3339), "initial_portions": n, "unit_cost_minor": 125, "currency": "THB"}, &batch, 201); err != nil {
			return err
		}
		ids = append(ids, batch.ID)
	}
	fmt.Println("Inventory REST: created 2 batches after Catalog gRPC validation")
	conn, err := grpc.NewClient(env("INVENTORY_GRPC_ADDR", "127.0.0.1:50051"), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()
	client := pb.NewInventoryServiceClient(conn)
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+orderToken)
	items := []*pb.PortionRequest{{FlavorId: flavor.ID, Portions: 3}}
	available, err := client.CheckAvailability(ctx, &pb.CheckAvailabilityRequest{Items: items})
	if err != nil {
		return err
	}
	if !available.AllAvailable || available.Items[0].AvailablePortions != 6 {
		return fmt.Errorf("unexpected availability: %v", available)
	}
	fmt.Println("gRPC CheckAvailability: 6 portions")
	req := &pb.ReservePortionsRequest{OrderId: uuid.NewString(), IdempotencyKey: uuid.NewString(), Items: items}
	reserved, err := client.ReservePortions(ctx, req)
	if err != nil {
		return err
	}
	a := reserved.Reservation.Allocations
	if len(a) != 2 || a[0].BatchId != ids[0] || a[0].Portions != 2 || a[1].Portions != 1 {
		return fmt.Errorf("unexpected FEFO allocations: %v", a)
	}
	fmt.Println("gRPC ReservePortions: FEFO allocates 2 early + 1 later")
	replay, err := client.ReservePortions(ctx, req)
	if err != nil || replay.Reservation.ReservationId != reserved.Reservation.ReservationId {
		return fmt.Errorf("idempotency replay failed: %v", err)
	}
	fmt.Println("gRPC retry: same reservation, no double deduction")
	if _, err = client.ConfirmReservation(ctx, &pb.ConfirmReservationRequest{ReservationId: reserved.Reservation.ReservationId, OrderId: req.OrderId, IdempotencyKey: uuid.NewString()}); err != nil {
		return err
	}
	fmt.Println("gRPC ConfirmReservation: 3 portions sold")
	req = &pb.ReservePortionsRequest{OrderId: uuid.NewString(), IdempotencyKey: uuid.NewString(), Items: []*pb.PortionRequest{{FlavorId: flavor.ID, Portions: 1}}}
	reserved, err = client.ReservePortions(ctx, req)
	if err != nil {
		return err
	}
	if _, err = client.ReleaseReservation(ctx, &pb.ReleaseReservationRequest{ReservationId: reserved.Reservation.ReservationId, OrderId: req.OrderId, IdempotencyKey: uuid.NewString(), Reason: "demo cancellation"}); err != nil {
		return err
	}
	fmt.Println("gRPC ReleaseReservation: cancelled order returns 1 portion")
	if err = request(ctx, "POST", rest+"/api/v1/inventory/batches/"+ids[1]+"/waste", token, map[string]any{"portions": 1, "reason": "QUALITY", "idempotency_key": uuid.NewString()}, nil, 201); err != nil {
		return err
	}
	fmt.Println("REST Waste: 1 portion; event persisted in outbox")
	var b struct {
		Initial   int64 `json:"initial_portions"`
		Available int64 `json:"available_portions"`
		Reserved  int64 `json:"reserved_portions"`
		Sold      int64 `json:"sold_portions"`
		Wasted    int64 `json:"wasted_portions"`
	}
	if err = request(ctx, "GET", rest+"/api/v1/inventory/batches/"+ids[1], token, nil, &b, 200); err != nil {
		return err
	}
	if b.Initial != 4 || b.Available != 2 || b.Reserved != 0 || b.Sold != 1 || b.Wasted != 1 {
		return fmt.Errorf("unexpected final balances: %+v", b)
	}
	fmt.Println("PASS: Catalog integration, REST, gRPC lifecycle, FEFO, idempotency, shared balances and waste. Demo batches/history remain.")
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
