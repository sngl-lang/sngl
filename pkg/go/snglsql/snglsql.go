// Package snglsql is the runtime behind sngl:sql's query and exec for a
// program compiled to Go, and for the interpreter.
//
// It holds no type of the program's: arguments arrive as a JSON array and
// rows leave as one, which the program's own codec reads. A driver is
// registered by its engine's package (snglsql/sqlite and the rest), which a
// program imports for the engines it queries, so a program querying SQLite
// links no PostgreSQL driver.
package snglsql

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Driver is how an engine is reached.
type Driver struct {
	// Name is the database/sql driver's.
	Name string
	// Code is the database's own code for err, or "" where it reported none.
	Code func(err error) string
	// Open adjusts a freshly opened pool.
	Open func(db *sql.DB)
	// Source is what the driver opens for a source, where that differs from
	// the source itself.
	Source func(source string) string
	// Arg is an argument as the driver takes it, given what its argKinds
	// entry says it stands for; nil hands each over as JSON decoded it.
	Arg func(v any, kind string) any
}

var (
	mu      sync.Mutex
	drivers = map[string]Driver{}
	pools   = map[[2]string]*sql.DB{}
)

// Register makes engine, as sngl:sql's Engine names it, reachable.
func Register(engine string, d Driver) {
	mu.Lock()
	defer mu.Unlock()
	drivers[engine] = d
}

// Failure is why a statement answered nothing. Code is set where the
// database reported the failure; Cancelled where ctx ended first.
type Failure struct {
	Cancelled bool
	Code      string
	Message   string
}

func pool(engine, source string) (*sql.DB, Driver, *Failure) {
	mu.Lock()
	defer mu.Unlock()
	d, ok := drivers[engine]
	if !ok {
		return nil, d, &Failure{Message: "no driver for " + engine + " is linked"}
	}
	key := [2]string{engine, source}
	if db, ok := pools[key]; ok {
		return db, d, nil
	}
	open := source
	if d.Source != nil {
		open = d.Source(source)
	}
	db, err := sql.Open(d.Name, open)
	if err != nil {
		return nil, d, &Failure{Message: err.Error()}
	}
	if d.Open != nil {
		d.Open(db)
	}
	pools[key] = db
	return db, d, nil
}

func failure(ctx context.Context, d Driver, err error) *Failure {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return &Failure{Cancelled: true, Message: "cancelled"}
	}
	f := &Failure{Message: err.Error()}
	if d.Code != nil {
		f.Code = d.Code(err)
	}
	return f
}

func decodeArgs(d Driver, args string, kinds []string) ([]any, error) {
	if strings.TrimSpace(args) == "" {
		return nil, nil
	}
	dec := json.NewDecoder(strings.NewReader(args))
	dec.UseNumber()
	var raw []any
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("arguments are not a JSON array: %w", err)
	}
	for i, v := range raw {
		switch v := v.(type) {
		case json.Number:
			if n, err := v.Int64(); err == nil {
				raw[i] = n
			} else if f, err := v.Float64(); err == nil {
				raw[i] = f
			}
		case map[string]any, []any:
			b, _ := json.Marshal(v)
			raw[i] = string(b)
		}
		if d.Arg != nil && i < len(kinds) && kinds[i] != "" {
			raw[i] = d.Arg(raw[i], kinds[i])
		}
	}
	return raw, nil
}

// Query runs text in ctx against the database engine opens at source and
// answers its rows as a JSON array of objects, each value under its place's
// name in columns, read as its place's kind says.
func Query(ctx context.Context, engine, source, text, args string, argKinds, columns, kinds []string) (string, *Failure) {
	if ctx.Err() != nil {
		return "", &Failure{Cancelled: true, Message: "cancelled"}
	}
	db, d, f := pool(engine, source)
	if f != nil {
		return "", f
	}
	values, err := decodeArgs(d, args, argKinds)
	if err != nil {
		return "", &Failure{Message: err.Error()}
	}
	rows, err := db.QueryContext(ctx, text, values...)
	if err != nil {
		return "", failure(ctx, d, err)
	}
	defer rows.Close()
	got, err := rows.Columns()
	if err != nil {
		return "", failure(ctx, d, err)
	}
	var b bytes.Buffer
	b.WriteByte('[')
	cells := make([]any, len(got))
	ptrs := make([]any, len(got))
	for i := range cells {
		ptrs[i] = &cells[i]
	}
	first := true
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return "", failure(ctx, d, err)
		}
		if !first {
			b.WriteByte(',')
		}
		first = false
		b.WriteByte('{')
		for i, v := range cells {
			if i > 0 {
				b.WriteByte(',')
			}
			name := got[i]
			if i < len(columns) {
				name = columns[i]
			}
			kind := ""
			if i < len(kinds) {
				kind = kinds[i]
			}
			key, _ := json.Marshal(name)
			b.Write(key)
			b.WriteByte(':')
			b.WriteString(Value(v, kind))
		}
		b.WriteByte('}')
	}
	if err := rows.Err(); err != nil {
		return "", failure(ctx, d, err)
	}
	b.WriteByte(']')
	return b.String(), nil
}

// Exec runs text in ctx as Query does, and answers how many rows it changed
// and the id of the last it inserted, where the driver gives one.
func Exec(ctx context.Context, engine, source, text, args string, argKinds []string) (int64, int64, *Failure) {
	if ctx.Err() != nil {
		return 0, 0, &Failure{Cancelled: true, Message: "cancelled"}
	}
	db, d, f := pool(engine, source)
	if f != nil {
		return 0, 0, f
	}
	values, err := decodeArgs(d, args, argKinds)
	if err != nil {
		return 0, 0, &Failure{Message: err.Error()}
	}
	res, err := db.ExecContext(ctx, text, values...)
	if err != nil {
		return 0, 0, failure(ctx, d, err)
	}
	rows, _ := res.RowsAffected()
	last, _ := res.LastInsertId()
	return rows, last, nil
}

// Value is v, a value database/sql scanned, as JSON, read as kind says.
func Value(v any, kind string) string {
	switch kind {
	case "bool":
		switch x := v.(type) {
		case int64:
			return strconv.FormatBool(x != 0)
		case []byte:
			return strconv.FormatBool(string(x) != "0" && string(x) != "" && string(x) != "f" && string(x) != "false")
		case string:
			return strconv.FormatBool(x != "0" && x != "" && x != "f" && x != "false")
		}
	case "datetime", "date":
		var t time.Time
		switch x := v.(type) {
		case time.Time:
			t = x
		case []byte:
			t, _ = ParseTime(string(x))
		case string:
			t, _ = ParseTime(x)
		case int64:
			t = time.Unix(x, 0).UTC()
		}
		if !t.IsZero() {
			if kind == "date" {
				return strconv.Quote(t.Format(time.DateOnly))
			}
			return strconv.Quote(t.Format(time.RFC3339Nano))
		}
	}
	switch x := v.(type) {
	case nil:
		return "null"
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		b, _ := json.Marshal(x)
		return string(b)
	case bool:
		return strconv.FormatBool(x)
	case time.Time:
		return strconv.Quote(x.Format(time.RFC3339Nano))
	case []byte:
		// Text a driver hands back as bytes is text; anything else is a blob,
		// written as base64, as sngl:json writes bytes.
		if !utf8.Valid(x) {
			return strconv.Quote(base64.StdEncoding.EncodeToString(x))
		}
		b, _ := json.Marshal(string(x))
		return string(b)
	case string:
		b, _ := json.Marshal(x)
		return string(b)
	}
	b, err := json.Marshal(v)
	if err != nil {
		b, _ = json.Marshal(fmt.Sprint(v))
	}
	return string(b)
}

func isNumber(b []byte) bool {
	_, err := strconv.ParseFloat(string(b), 64)
	return err == nil
}

func utf8Valid(b []byte) bool { return strings.ToValidUTF8(string(b), "�") == string(b) }

// timeLayouts are the forms SQLite's date functions and drivers write.
var timeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02T15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04",
	"2006-01-02T15:04",
	time.DateOnly,
}

// ParseTime reads text in any of the forms SQLite stores a time as.
func ParseTime(text string) (time.Time, bool) {
	text = strings.TrimSuffix(strings.TrimSpace(text), "Z")
	for _, l := range timeLayouts {
		if t, err := time.Parse(l, text); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}
