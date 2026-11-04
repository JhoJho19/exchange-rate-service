package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/JhoJho19/exchange-rate-service/internal/apperrors"
	"github.com/JhoJho19/exchange-rate-service/internal/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type idempotencyRow func(...any) error

func (f idempotencyRow) Scan(dest ...any) error { return f(dest...) }

type idempotencyConnection struct {
	ownershipConnection
	query func(string, ...any) pgx.Row
}

func (c idempotencyConnection) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	return c.query(sql, args...)
}
func (c idempotencyConnection) IsClosed() bool { return false }

func TestCreateOrGet(t *testing.T) {
	dbErr := errors.New("database unavailable")
	pair := models.CurrencyPair{BaseCurrency: "EUR", QuoteCurrency: "MXN"}
	for _, tc := range []struct {
		name                               string
		firstErr, errorInsert, errorLookup error
		conflict, created                  bool
		queries, prepares                  int
	}{
		{name: "existing", queries: 1},
		{name: "conflict", conflict: true, queries: 1},
		{name: "new", firstErr: pgx.ErrNoRows, created: true, queries: 2, prepares: 1},
		{name: "concurrent winner", firstErr: pgx.ErrNoRows, errorInsert: pgx.ErrNoRows, queries: 3, prepares: 1},
		{name: "concurrent conflict", firstErr: pgx.ErrNoRows, errorInsert: pgx.ErrNoRows, conflict: true, queries: 3, prepares: 1},
		{name: "lookup error", firstErr: dbErr, queries: 1},
		{name: "insert error", firstErr: pgx.ErrNoRows, errorInsert: dbErr, queries: 2, prepares: 1},
		{name: "conflict lookup error", firstErr: pgx.ErrNoRows, errorInsert: pgx.ErrNoRows, errorLookup: dbErr, queries: 3, prepares: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := uuid.New()
			queries, prepares := 0, 0
			conn := idempotencyConnection{query: func(sql string, args ...any) pgx.Row {
				queries++
				call := queries
				return idempotencyRow(func(dest ...any) error {
					if call == 1 && tc.firstErr != nil {
						return tc.firstErr
					}
					if strings.Contains(sql, "INSERT") {
						if !strings.Contains(sql, "ON CONFLICT (idempotency_key) DO NOTHING RETURNING id") || args[5] != "opaque key" {
							t.Fatal("incorrect insert")
						}
						if tc.errorInsert != nil {
							return tc.errorInsert
						}
					} else {
						if call == 3 && tc.errorLookup != nil {
							return tc.errorLookup
						}
						*dest[1].(*string) = pair.BaseCurrency
						*dest[2].(*string) = pair.QuoteCurrency
						if tc.conflict {
							*dest[2].(*string) = "USD"
						}
					}
					*dest[0].(*uuid.UUID) = id
					return nil
				})
			}}
			r := &TaskRepository{owner: &ownership{conn: conn, ctx: context.Background(), gate: make(chan struct{}, 1), prepared: true}}
			got, created, err := r.CreateOrGet(context.Background(), "opaque key", pair, func() (models.UpdateTask, error) {
				prepares++
				return models.UpdateTask{ID: id, Pair: pair, Status: models.StatusPending}, nil
			})
			wantErr := error(nil)
			if tc.conflict {
				wantErr = apperrors.ErrIdempotencyConflict
			}
			if tc.firstErr == dbErr || tc.errorInsert == dbErr || tc.errorLookup == dbErr {
				wantErr = dbErr
			}
			if !errors.Is(err, wantErr) || created != tc.created || queries != tc.queries || prepares != tc.prepares {
				t.Fatalf("result %s %t %v; queries %d prepares %d", got, created, err, queries, prepares)
			}
			if err == nil && got != id {
				t.Fatal("wrong ID")
			}
		})
	}
}
