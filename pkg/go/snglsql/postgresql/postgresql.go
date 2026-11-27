// Package snglpostgresql registers PostgreSQL with snglsql, through pgx.
package snglpostgresql

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"

	"duckfam.us/sngl/pkg/go/snglsql"
)

// Engine is the engine as sngl:sql names it: what a program hands snglsql,
// which links this package into it.
const Engine = "postgresql"

func init() {
	snglsql.Register(Engine, snglsql.Driver{Name: "pgx", Code: code})
}

func code(err error) string {
	if e, ok := errors.AsType[*pgconn.PgError](err); ok {
		return e.Code
	}
	return ""
}
