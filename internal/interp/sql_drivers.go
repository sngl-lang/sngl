//go:build !js

package interp

import (
	_ "duckfam.us/sngl/pkg/go/snglsql/mysql"
	_ "duckfam.us/sngl/pkg/go/snglsql/postgresql"
	_ "duckfam.us/sngl/pkg/go/snglsql/sqlite"
)
