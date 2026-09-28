package transport

import (
	"batch-inventory-service/internal/inventory"
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/grpc/codes"
	"testing"
)

func TestErrorClassificationDoesNotLeakDependencyDetails(t *testing.T) {
	for _, tc := range []struct {
		err  error
		http int
		grpc codes.Code
	}{
		{context.DeadlineExceeded, 504, codes.DeadlineExceeded},
		{context.Canceled, 408, codes.Canceled},
		{inventory.ErrStock, 409, codes.FailedPrecondition},
		{inventory.ErrDependency, 503, codes.Unavailable},
		{&pgconn.PgError{Code: "57P01", Message: "private database address"}, 503, codes.Unavailable},
		{&pgconn.PgError{Code: "40P01", Message: "private SQL"}, 503, codes.Unavailable},
		{errors.New("private stored credentials"), 500, codes.Internal},
	} {
		http, grpc, _, msg := classify(tc.err)
		if http != tc.http || grpc != tc.grpc {
			t.Fatalf("classification: %d %s", http, grpc)
		}
		if msg == tc.err.Error() {
			t.Fatal("raw error leaked")
		}
	}
}
