package utils

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

// IsUniqueConstraintViolation reports whether err is a unique constraint (23505) violation.
// Prefers Gorm's translated error when available (TranslateError=true), but also handles
// raw driver errors (pgx / libpq) by SQLSTATE code.
func IsUniqueConstraintViolation(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return true
	}

	var pqErr *pq.Error
	if errors.As(err, &pqErr) && string(pqErr.Code) == "23505" {
		return true
	}

	return false
}
