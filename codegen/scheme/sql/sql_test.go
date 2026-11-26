package sql

import (
	"slices"
	"strings"
	"testing"
)

func generate(t *testing.T, engine, schema, queries string) *Package {
	t.Helper()
	config := "version: \"2\"\nsql:\n  - engine: " + engine + "\n    schema: schema.sql\n    queries: query.sql\n"
	p, err := Generate(map[string]string{"db/schema.sql": schema, "db/query.sql": queries}, config, "sqlc.yaml", "./db/sqlc.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func contains(t *testing.T, src string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(src, w) {
			t.Errorf("missing %q in\n%s", w, src)
		}
	}
}

func TestPostgres(t *testing.T) {
	p := generate(t, "postgresql", `
CREATE TYPE mood AS ENUM ('happy', 'not-sure', 'sad');
CREATE TABLE people (
  id      bigserial PRIMARY KEY,
  age     integer NOT NULL,
  height  double precision,
  mood    mood NOT NULL,
  born    date,
  seen    timestamptz NOT NULL,
  balance numeric NOT NULL,
  tags    text[] NOT NULL,
  func    text
);
CREATE FUNCTION touch() RETURNS trigger AS $$
BEGIN
  NEW.seen := now();
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
`, `
-- name: GetPerson :one
SELECT id, age, height, mood, born, seen, balance::text AS balance, func FROM people WHERE id = $1;

-- name: Balances :many
SELECT balance FROM people;

-- name: Tags :many
SELECT tags FROM people;

-- name: Moods :many
SELECT DISTINCT mood FROM people WHERE age > sqlc.arg(min_age);

-- name: Copy :copyfrom
INSERT INTO people (age, mood, seen, balance, tags) VALUES ($1, $2, $3, $4, $5);
`)
	contains(t, p.Source,
		"import time \"sngl:time\"",
		"context #source(\"\")",
		"enum Mood {\n    happy\n    #[json.identifier(\"not-sure\")]\n    notSure\n    sad\n}",
		"struct GetPersonRow {\n    id      int64\n    age     int\n    height  option[float]\n    mood    Mood\n    born    option[time.date]\n    seen    time.datetime\n    balance string\n    func_   option[string]\n}",
		"func getPersonCall(id int64, life async.Lifetime = async.forever) async.Result[option[GetPersonRow]]",
		`kinds=["", "", "", "", "date", "datetime", "", ""]`,
	)
	note := "query Balances is left out: its column balance is a numeric, which SNGL has no exact number for yet: cast it in the query, `::text` or, where rounding is fine, `::float8`"
	if !slices.Contains(p.Notes, note) {
		t.Errorf("missing note %q in %q", note, p.Notes)
	}
}

func TestPostgresNotes(t *testing.T) {
	p := generate(t, "postgresql", "CREATE TABLE people (id bigserial PRIMARY KEY, tags text[] NOT NULL);", `
-- name: Tags :many
SELECT tags FROM people;

-- name: Copy :copyfrom
INSERT INTO people (tags) VALUES ($1);

-- name: ByIDs :many
SELECT id FROM people WHERE id = ANY(sqlc.slice(ids)::bigint[]);
`)
	want := []string{
		"column tags of people is left out of its struct: is an array, which sngl:sql does not read yet",
		"query Tags is left out: its column tags is an array, which sngl:sql does not read yet",
		"query Copy is left out: :copyfrom is not one sngl:sql runs",
	}
	for _, w := range want {
		if !slices.Contains(p.Notes, w) {
			t.Errorf("missing note %q in %q", w, p.Notes)
		}
	}
	if strings.Contains(p.Source, "tagsCall") || strings.Contains(p.Source, "copyCall") {
		t.Errorf("a query left out was written:\n%s", p.Source)
	}
}

func TestSQLiteStatements(t *testing.T) {
	p := generate(t, "sqlite", `
-- +goose Up
CREATE TABLE books (id INTEGER PRIMARY KEY, title TEXT NOT NULL, lent BOOLEAN NOT NULL);
/* a comment; with a semicolon */
CREATE TRIGGER t AFTER INSERT ON books BEGIN
  UPDATE books SET title = CASE WHEN title = '' THEN 'untitled;' ELSE title END WHERE id = new.id;
END;
CREATE INDEX books_title ON books (title);
-- +goose Down
DROP TABLE books;
`, `
-- name: CountBooks :one
SELECT count(*) FROM books;

-- name: Lent :many
SELECT id, lent FROM books WHERE lent;
`)
	contains(t, p.Source,
		"context #source(\":memory:\")",
		`"CREATE TABLE books (id INTEGER PRIMARY KEY, title TEXT NOT NULL, lent BOOLEAN NOT NULL)",`,
		`"CREATE TRIGGER t AFTER INSERT ON books BEGIN\n  UPDATE books SET title = CASE WHEN title = '' THEN 'untitled;' ELSE title END WHERE id = new.id;\nEND",`,
		`"CREATE INDEX books_title ON books (title)",`,
		"func countBooksCall(life async.Lifetime = async.forever) async.Result[option[int64]]",
		"return {ok=r.count}",
		`kinds=["", "bool"]`,
	)
	if strings.Contains(p.Source, "DROP TABLE") {
		t.Errorf("the down migration was kept:\n%s", p.Source)
	}
}

func TestMySQL(t *testing.T) {
	p := generate(t, "mysql", `
CREATE TABLE books (
  id    bigint NOT NULL AUTO_INCREMENT PRIMARY KEY,
  pages int NOT NULL,
  lent  tinyint(1) NOT NULL,
  added datetime
);
`, `
-- name: AddBook :execlastid
INSERT INTO books (pages, lent) VALUES (?, ?);

-- name: Lend :execrows
UPDATE books SET lent = 1 WHERE id = ?;

-- name: Label :one
SELECT CAST(id AS CHAR) AS label FROM books WHERE id = ?;
`)
	contains(t, p.Source,
		"struct Book {\n    id    int64\n    pages int\n    lent  bool\n    added option[time.datetime]\n}",
		"func addBookCall(pages int, lent bool, life async.Lifetime = async.forever) async.Result[int64]",
		"ok { return {ok=r.lastId} }",
		"func lendCall(id int64, life async.Lifetime = async.forever) async.Result[int64]",
		"ok { return {ok=r.rows} }",
		"struct LabelRow {\n    label string\n}",
	)
}

func TestErrors(t *testing.T) {
	config := "version: \"2\"\nsql:\n  - engine: sqlite\n    schema: schema.sql\n    queries: query.sql\n"
	_, err := Generate(map[string]string{"db/schema.sql": "CREATE TABLE a (id INTEGER);", "db/query.sql": "-- name: Bad :one\nSELECT nope FROM a;\n"}, config, "sqlc.yaml", "./db/sqlc.yaml")
	if err == nil || err.Error() != `db/query.sql:2:8: column "nope" does not exist` {
		t.Errorf("error = %v", err)
	}
	two := config + "  - engine: sqlite\n    schema: schema.sql\n    queries: query.sql\n"
	_, err = Generate(map[string]string{"db/schema.sql": "CREATE TABLE a (id INTEGER);", "db/query.sql": "-- name: All :many\nSELECT id FROM a;\n"}, two, "sqlc.yaml", "./db/sqlc.yaml")
	if err == nil || !strings.Contains(err.Error(), "declares 2 packages, and an import reads one") {
		t.Errorf("error = %v", err)
	}
}

func TestSplit(t *testing.T) {
	for _, c := range []struct {
		text string
		want []string
	}{
		{"a; b;", []string{"a", "b"}},
		{"a -- x; y\n; b", []string{"a -- x; y", "b"}},
		{"-- lead\n/* in */ a;", []string{"a"}},
		{"BEGIN; CREATE TABLE t (x int); COMMIT;", []string{"BEGIN", "CREATE TABLE t (x int)", "COMMIT"}},
		{"CREATE FUNCTION f() AS $body$ x; y $body$; z", []string{"CREATE FUNCTION f() AS $body$ x; y $body$", "z"}},
		{"SELECT $1; SELECT 'it''s; fine'", []string{"SELECT $1", "SELECT 'it''s; fine'"}},
		{"-- only a comment\n;", nil},
		{"CREATE TABLE \"a;b\" (`c;d` int, [e;f] int)", []string{"CREATE TABLE \"a;b\" (`c;d` int, [e;f] int)"}},
	} {
		if got := split(c.text); !slices.Equal(got, c.want) {
			t.Errorf("split(%q) = %q, want %q", c.text, got, c.want)
		}
	}
}

func TestInputs(t *testing.T) {
	got, err := Inputs("version: \"2\"\nsql:\n  - engine: sqlite\n    schema: [\"./schema.sql\", ../migrations]\n    queries: \"queries/*.sql\"\n", "./db")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"db/schema.sql", "migrations", "db/queries/*.sql"}; !slices.Equal(got, want) {
		t.Errorf("Inputs = %q, want %q", got, want)
	}
}

func TestConfigFor(t *testing.T) {
	got, err := ConfigFor("-- sngl:schema schema.sql\n-- sngl:schema migrations\n\n-- name: A :one\nSELECT 1;\n", "queries.sql", "./db/queries.sql", "mysql")
	if err != nil {
		t.Fatal(err)
	}
	want := "version: \"2\"\nsql:\n  - engine: mysql\n    schema: [\"schema.sql\", \"migrations\"]\n    queries: [\"queries.sql\"]\n"
	if got != want {
		t.Errorf("ConfigFor = %q, want %q", got, want)
	}
	// The query file's own engine overrides its schema's.
	got, _ = ConfigFor("-- sngl:schema schema.sql\n-- sngl:engine postgresql\n", "q.sql", "q.sql", "mysql")
	if !strings.Contains(got, "engine: postgresql") {
		t.Errorf("ConfigFor = %q, want postgresql", got)
	}
}

func TestSchemaEngine(t *testing.T) {
	files := map[string]string{
		"db/queries.sql":  "-- sngl:schema schema\n",
		"db/schema/a.sql": "-- sngl:engine postgresql\nCREATE TABLE a (id int);",
		"db/schema/b.sql": "CREATE TABLE b (id int);",
		"db/schema/c.sql": "-- sngl:engine postgresql\n",
	}
	if got, err := schemaEngine(files, "db/queries.sql"); err != nil || got != "postgresql" {
		t.Errorf("schemaEngine = %q, %v", got, err)
	}
	files["db/schema/c.sql"] = "-- sngl:engine mysql\n"
	if _, err := schemaEngine(files, "db/queries.sql"); err == nil || err.Error() != "db/schema/c.sql:1: sngl:engine mysql, where db/schema/a.sql:1 says postgresql" {
		t.Errorf("schemaEngine error = %v", err)
	}
	if got, err := schemaEngine(map[string]string{"q.sql": "", "s.sql": "CREATE TABLE a (id int);"}, "q.sql"); err != nil || got != "sqlite" {
		t.Errorf("schemaEngine = %q, %v", got, err)
	}
}

func TestComponents(t *testing.T) {
	p := generate(t, "postgresql", "CREATE TABLE a (id bigserial PRIMARY KEY, at timestamptz NOT NULL, name text NOT NULL);", `
-- name: Add :one
INSERT INTO a (at, name) VALUES ($1, $2) RETURNING id;

-- name: Named :many
SELECT id FROM a WHERE name = $1;

-- name: Since :many
SELECT id FROM a WHERE at > $1;

-- name: Recent :many
WITH r AS (SELECT id FROM a ORDER BY at DESC LIMIT 5) SELECT id FROM r;
`)
	contains(t, p.Source, "component named[F any](", "component recent[F any](", "func addCall(", "func sinceCall(")
	for _, absent := range []string{"component add[", "component since["} {
		if strings.Contains(p.Source, absent) {
			t.Errorf("%s written:\n%s", absent, p.Source)
		}
	}
	if !slices.Contains(p.Notes, "query Since has no component: its parameter at is a time.datetime, and a query's key is compared") {
		t.Errorf("notes = %q", p.Notes)
	}
	for text, want := range map[string]bool{
		"SELECT 1":                              true,
		"-- c\nselect 1":                        true,
		"INSERT INTO a VALUES (1) RETURNING id": false,
		"UPDATE a SET b = 1 RETURNING id":       false,
		"WITH d AS (DELETE FROM a RETURNING id) SELECT id FROM d": false,
	} {
		if got := onlyReads(text); got != want {
			t.Errorf("onlyReads(%q) = %v", text, got)
		}
	}
}
