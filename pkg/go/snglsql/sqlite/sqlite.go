// Package snglsqlite registers SQLite with snglsql, through modernc.org/sqlite,
// which needs no cgo.
package snglsqlite

import (
	"database/sql"
	"errors"
	"strings"

	"modernc.org/sqlite"

	"duckfam.us/sngl/pkg/go/snglsql"
)

// Engine is the engine as sngl:sql names it: what a program hands snglsql,
// which links this package into it.
const Engine = "sqlite"

func init() {
	snglsql.Register(Engine, snglsql.Driver{
		Name: "sqlite",
		Code: code,
		// One connection: each of a pool's would open its own `:memory:`
		// database, and a write from one is not seen by the next.
		Open: func(db *sql.DB) { db.SetMaxOpenConns(1) },
		// `:memory:` and any name after it is a database of its own, held
		// by its one connection.
		Source: func(s string) string {
			if strings.HasPrefix(s, ":memory:") {
				return ":memory:"
			}
			return s
		},
	})
}

var primary = map[int]string{
	1: "SQLITE_ERROR", 2: "SQLITE_INTERNAL", 3: "SQLITE_PERM", 4: "SQLITE_ABORT",
	5: "SQLITE_BUSY", 6: "SQLITE_LOCKED", 7: "SQLITE_NOMEM", 8: "SQLITE_READONLY",
	9: "SQLITE_INTERRUPT", 10: "SQLITE_IOERR", 11: "SQLITE_CORRUPT", 12: "SQLITE_NOTFOUND",
	13: "SQLITE_FULL", 14: "SQLITE_CANTOPEN", 15: "SQLITE_PROTOCOL", 17: "SQLITE_SCHEMA",
	18: "SQLITE_TOOBIG", 19: "SQLITE_CONSTRAINT", 20: "SQLITE_MISMATCH", 21: "SQLITE_MISUSE",
	23: "SQLITE_AUTH", 25: "SQLITE_RANGE", 26: "SQLITE_NOTADB",
}

func code(err error) string {
	var e *sqlite.Error
	if !errors.As(err, &e) {
		return ""
	}
	return primary[e.Code()&0xff]
}
