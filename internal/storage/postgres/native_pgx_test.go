package postgres

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"testing"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPGXNumericRoundTripsMaxUint64(t *testing.T) {
	typeMap := pgtype.NewMap()
	encoded, err := typeMap.Encode(pgtype.NumericOID, pgtype.BinaryFormatCode, uint64(math.MaxUint64), nil)
	if err != nil {
		t.Fatalf("encode max uint64: %v", err)
	}
	var decoded uint64
	if err = typeMap.Scan(pgtype.NumericOID, pgtype.BinaryFormatCode, encoded, &decoded); err != nil {
		t.Fatalf("decode max uint64: %v", err)
	}
	if decoded != math.MaxUint64 {
		t.Fatalf("decoded = %d, want %d", decoded, uint64(math.MaxUint64))
	}
}

func TestNormalizeDatabaseValuePreservesNumericString(t *testing.T) {
	value := pgtype.Numeric{Int: new(big.Int).SetUint64(math.MaxUint64), Valid: true}
	if got := normalizeDatabaseValue(value); got != "18446744073709551615" {
		t.Fatalf("normalizeDatabaseValue = %#v, want decimal string", got)
	}
}

func TestUniqueViolationUsesPostgreSQLState(t *testing.T) {
	duplicate := fmt.Errorf("insert room: %w", &pgconn.PgError{Code: pgerrcode.UniqueViolation})
	if !isUniqueViolation(duplicate) {
		t.Fatal("wrapped unique violation was not recognized")
	}
	if isUniqueViolation(&pgconn.PgError{Code: pgerrcode.ForeignKeyViolation}) {
		t.Fatal("foreign-key violation was recognized as unique violation")
	}
}

func TestClosedPlatformRejectsModuleDatabaseOpen(t *testing.T) {
	platform := &Platform{moduleDBs: make(map[string]*pgxpool.Pool)}
	if err := platform.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if _, err := platform.openModuleDatabase(context.Background(), "module"); err == nil {
		t.Fatal("openModuleDatabase returned nil error after Close")
	}
}
