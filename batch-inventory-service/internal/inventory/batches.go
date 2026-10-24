package inventory

import (
	"batch-inventory-service/internal/service"
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"math"
	"strings"
	"time"
)

func (s *Service) Create(ctx context.Context, in CreateInput) (Batch, error) {
	var b Batch
	if who := currentActor(ctx); who.id == "" || who.role != "MANAGER" {
		return b, wrapInvalid("trusted Manager actor required")
	}
	var err error
	in.FlavorID, err = ID(in.FlavorID)
	if err != nil {
		return b, wrapInvalid("flavor_id")
	}
	batchID := uuid.NewString()
	code, err := batchCode(in.BatchCode, batchID)
	if err != nil {
		return b, err
	}
	date, err := time.Parse("2006-01-02", in.ProductionDate)
	if err != nil || in.ExpiresAt.IsZero() || in.ExpiresAt.Before(date) || in.Initial < 1 || in.Initial > MaxPortions || in.UnitCost == nil || *in.UnitCost < 0 || *in.UnitCost > math.MaxInt64/in.Initial || in.Currency != "THB" {
		return b, ErrInvalid
	}
	if s.Catalog == nil {
		return b, ErrDependency
	}
	if err = s.Catalog.CheckActive(ctx, in.FlavorID); err != nil {
		return b, err
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return b, err
	}
	defer rollback(tx)
	if err = lockFlavors(ctx, tx, []string{in.FlavorID}); err != nil {
		return b, err
	}
	t, err := now(ctx, tx)
	if err != nil {
		return b, err
	}
	if !in.ExpiresAt.After(t) {
		return b, wrapInvalid("expires_at")
	}
	b = Batch{ID: batchID, BatchCode: code, FlavorID: in.FlavorID, ProductionDate: in.ProductionDate, ExpiresAt: in.ExpiresAt.UTC(), Initial: in.Initial, Available: in.Initial, UnitCost: *in.UnitCost, Currency: "THB", Status: "ACTIVE", CreatedAt: t, UpdatedAt: t}
	_, err = tx.Exec(ctx, `INSERT INTO batches(id,batch_code,flavor_id,production_date,expires_at,initial_portions,available_portions,unit_cost_minor,currency,status,created_at,updated_at) VALUES($1,$2,$3,$4::text::date,$5,$6,$6,$7,'THB','ACTIVE',$8,$8)`, b.ID, b.BatchCode, b.FlavorID, b.ProductionDate, b.ExpiresAt, b.Initial, b.UnitCost, t)
	if err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" && pe.ConstraintName == "batches_batch_code_key" {
			return Batch{}, ErrBatchCodeConflict
		}
		return Batch{}, err
	}
	if err = movement(ctx, tx, b.ID, "", "PRODUCED", b.Initial, t); err != nil {
		return Batch{}, err
	}
	_ = service.ResetLowStockNotified(ctx, tx, b.FlavorID, t)
	return b, tx.Commit(ctx)
}

// Codes label production containers; UUIDs remain the resource identifiers.
// Omission keeps legacy callers compatible without assigning an empty label.
func batchCode(raw *string, batchID string) (string, error) {
	if raw == nil {
		return "BATCH-" + strings.ToUpper(batchID), nil
	}
	code := strings.TrimSpace(*raw)
	if len(code) == 0 || len(code) > 64 {
		return "", wrapInvalid("batch_code")
	}
	for i, c := range []byte(code) {
		alnum := c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9'
		if !alnum && (i == 0 || c != '_' && c != '-') {
			return "", wrapInvalid("batch_code")
		}
	}
	return strings.ToUpper(code), nil
}
func (s *Service) Get(ctx context.Context, id string) (Batch, error) {
	id, err := ID(id)
	if err != nil {
		return Batch{}, err
	}
	b, err := scanBatch(s.DB.QueryRow(ctx, "SELECT "+batchColumns+" FROM batches WHERE id=$1", id))
	if err == nil {
		b.Status = batchStatus(b, time.Now().UTC())
	}
	return b, err
}
func (s *Service) List(ctx context.Context, flavor, status string) ([]Batch, error) {
	if flavor != "" {
		var err error
		flavor, err = ID(flavor)
		if err != nil {
			return nil, err
		}
	}
	if status != "" && status != "ACTIVE" && status != "EXHAUSTED" && status != "EXPIRED" && status != "ARCHIVED" {
		return nil, ErrInvalid
	}
	// Derived expiry also applies before the background sweep runs.
	rows, err := s.DB.Query(ctx, `SELECT `+batchColumns+` FROM batches WHERE ($1='' OR flavor_id::text=$1) AND ($2='' OR (CASE WHEN status='ARCHIVED' THEN 'ARCHIVED' WHEN expires_at<=statement_timestamp() THEN 'EXPIRED' WHEN available_portions=0 THEN 'EXHAUSTED' ELSE 'ACTIVE' END)=$2) ORDER BY expires_at,created_at,id`, flavor, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Batch{}
	for rows.Next() {
		b, err := scanBatch(rows)
		if err != nil {
			return nil, err
		}
		b.Status = batchStatus(b, time.Now().UTC())
		out = append(out, b)
	}
	return out, rows.Err()
}
func (s *Service) Availability(ctx context.Context, flavor string) ([]Availability, time.Time, error) {
	if flavor != "" {
		var err error
		flavor, err = ID(flavor)
		if err != nil {
			return nil, time.Time{}, err
		}
	}
	// The timestamp and values are read in a single statement/snapshot.
	rows, err := s.DB.Query(ctx, `SELECT flavor_id::text,COALESCE(sum(available_portions) FILTER(WHERE status IN ('ACTIVE','EXHAUSTED') AND expires_at>statement_timestamp()),0)::bigint,min(expires_at) FILTER(WHERE available_portions>0 AND status='ACTIVE' AND expires_at>statement_timestamp()),statement_timestamp() FROM batches WHERE ($1='' OR flavor_id::text=$1) GROUP BY flavor_id ORDER BY flavor_id`, flavor)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer rows.Close()
	out := []Availability{}
	asOf := time.Now().UTC()
	for rows.Next() {
		var a Availability
		if err = rows.Scan(&a.FlavorID, &a.Available, &a.EarliestExpiry, &asOf); err != nil {
			return nil, time.Time{}, err
		}
		out = append(out, a)
	}
	if err = rows.Err(); err != nil {
		return nil, time.Time{}, err
	}
	return out, asOf.UTC(), nil
}
