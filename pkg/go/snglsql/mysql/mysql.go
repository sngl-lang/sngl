// Package snglmysql registers MySQL with snglsql, through go-sql-driver/mysql.
package snglmysql

import (
	"errors"

	"github.com/go-sql-driver/mysql"

	"duckfam.us/sngl/pkg/go/snglsql"
)

// Engine is the engine as sngl:sql names it: what a program hands snglsql,
// which links this package into it.
const Engine = "mysql"

func init() {
	snglsql.Register(Engine, snglsql.Driver{Name: "mysql", Code: code})
}

func code(err error) string {
	var e *mysql.MySQLError
	if errors.As(err, &e) && e.SQLState != [5]byte{} {
		return string(e.SQLState[:])
	}
	return ""
}
