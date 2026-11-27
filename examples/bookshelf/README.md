# Bookshelf

A reading list kept in SQLite: a book added at the top, a search over the
shelf, and a row per book to mark read or take off. It builds for bubbletea,
fyne, gtk4 and android, and the interpreter runs it. The browser has no
database to reach, and html refuses it.

```
sngl generate --platform bubbletea --lang go     -o /tmp/shelf-tui examples/bookshelf
sngl generate --platform android   --lang kotlin -o /tmp/shelf-apk examples/bookshelf
```

## How it is put together

| file                  | what is in it                                                                  |
|-----------------------|--------------------------------------------------------------------------------|
| `app.sngl`            | the window, which creates the schema in its file, and the shelf                |
| `db/sqlc.yaml`        | sqlc's configuration: the engine, the schema and the queries                   |
| `db/schema.sql`       | the table and its index, each created only where it is not already             |
| `db/queries.sql`      | the queries, each named for the function it becomes, and where their schema is |
| `bookshelf_test.sngl` | the tests, each against a database of its own                                  |

**The database is an import.** `import books "sql:./db/queries.sql"` hands
[sqlc](https://sqlc.dev) the queries and the schema their first line names,
`-- sngl:schema schema.sql`, as `sqlc generate` would, and writes a SNGL
package from what it says of them. The engine is the schema's to say,
`-- sngl:engine postgresql`, and SQLite where it says none, as this one does;
a sqlc configuration, `sql:./db/sqlc.yaml`, is read as well. The package
holds a struct per table
(`books.Book`), an async function per query (`books.addBookCall(title=…)`,
answering an `async.Result`), and, for each query answering rows, a component
that asks when it mounts and whenever a parameter changes. sqlc checks every
query against the schema when the program is checked, so a column renamed in
`schema.sql` is an error at the query that names it.

`books.source` is a context naming the database: a file, or `:memory:` for one
held while the program runs. The window provides `bookshelf.db`, which on
android is under the app's own database directory; each test provides a
`:memory:` database of its own, `:memory:adds`, so no test sees another's
books.

`books.create()` runs the schema's statements, which is why each says
`IF NOT EXISTS`: the window runs it every time the app starts.
