package testsql

import (
	"database/sql"
	"testing"
)

func TestFresh(t *testing.T) {
	for _, engine := range []string{"postgresql", "mysql"} {
		t.Run(engine, func(t *testing.T) {
			if !Available(engine) {
				t.Skip("no " + engine + " server on PATH")
			}
			t.Cleanup(Stop)
			if err := Fresh(engine, "probe"); err != nil {
				t.Fatal(err)
			}
			driver := "pgx"
			if engine == "mysql" {
				driver = "mysql"
			}
			db, err := sql.Open(driver, Source(engine, "probe"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec("CREATE TABLE a (id int)"); err != nil {
				t.Fatal(err)
			}
			// A second Fresh empties the database.
			if err := Fresh(engine, "probe"); err != nil {
				t.Fatal(err)
			}
			var n int
			if err := db.QueryRow("SELECT count(*) FROM information_schema.tables WHERE table_name = 'a'").Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Errorf("table a survived Fresh")
			}
		})
	}
}
