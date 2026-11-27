// Package sql writes the package a `sql:` import resolves to: what sqlc's
// analysis of a configuration's schema and queries says, as a struct per
// table, an enum per enum and a function per query over sngl:sql.
package sql

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/sqlc-dev/sqlc/pkg/analyze"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/internal/parser"
)

// Package is what an import of a configuration resolves to.
type Package struct {
	Source string
	Notes  []string
}

// engines are the engines sngl:sql's runtime reaches, as sqlc names them.
var engines = []string{"sqlite", "postgresql", "mysql"}

// reserved are the names the written package declares beside the queries'.
var reserved = []string{"source", "schema", "create", "_rows", "_one"}

// reservedProps are the names an adapter's own props take.
var reservedProps = []string{"value", "pending", "failure", "load", "fail", "refresh", "query"}

// Inputs are the paths a configuration's packages read, each a `.sql` file,
// a directory of them, or a pattern sqlc expands, as the import root holds
// them: written relative to the configuration, which is in dir.
func Inputs(config, dir string) ([]string, error) {
	pkgs, err := analyze.Config(strings.NewReader(config))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, p := range pkgs {
		for _, f := range slices.Concat(p.Schema, p.Queries) {
			f = path.Join(dir, filepath.ToSlash(f))
			if !slices.Contains(out, f) {
				out = append(out, f)
			}
		}
	}
	return out, nil
}

// Generate analyses config, the text of the configuration named name beside
// from, over files, each `.sql` file it reads by its path under the import
// root, as Inputs answers it, and writes the package it describes, read from
// the import path from.
//
// A query file, name ending `.sql`, is config: its configuration is the one
// its directives say, for the engine it or its schema names.
func Generate(files map[string]string, config, name, from string) (*Package, error) {
	if strings.HasSuffix(name, ".sql") {
		engine, err := schemaEngine(files, path.Clean(from))
		if err != nil {
			return nil, err
		}
		if config, err = ConfigFor(config, name, from, engine); err != nil {
			return nil, err
		}
		name = "sqlc.yaml"
	}
	declared, err := analyze.Config(strings.NewReader(config))
	if err != nil {
		return nil, fmt.Errorf("sql:%s: %w", from, err)
	}
	if len(declared) != 1 {
		return nil, fmt.Errorf("sql:%s declares %d packages, and an import reads one: write a configuration per package", from, len(declared))
	}
	if e := declared[0].Engine; !slices.Contains(engines, e) {
		return nil, fmt.Errorf("sql:%s is written for %s, and sngl:sql reaches %s", from, e, strings.Join(engines, ", "))
	}
	dir, err := os.MkdirTemp("", "sngl-sql-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	for rel, text := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if !strings.HasPrefix(p, dir+string(filepath.Separator)) {
			return nil, fmt.Errorf("sql:%s: %s is outside the project", from, rel)
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			return nil, err
		}
	}
	conf := filepath.Join(dir, filepath.FromSlash(path.Dir(path.Clean(from))), path.Base(name))
	if err := os.MkdirAll(filepath.Dir(conf), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(conf, []byte(config), 0o644); err != nil {
		return nil, err
	}
	pkgs, err := analyze.Analyze(context.Background(), conf)
	if err != nil {
		if ae, ok := errors.AsType[*analyze.Error](err); ok {
			var lines []string
			for _, e := range ae.Errs {
				lines = append(lines, fmt.Sprintf("%s:%d:%d: %s", path.Join(path.Dir(from), filepath.ToSlash(e.Filename)), e.Line, e.Column, e.Err))
			}
			return nil, errors.New(strings.Join(lines, "\n"))
		}
		return nil, fmt.Errorf("sql:%s: %s", from, strings.ReplaceAll(err.Error(), dir+string(filepath.Separator), ""))
	}
	w := newWriter(pkgs[0], from)
	src, err := w.write()
	if err != nil {
		return nil, err
	}
	return &Package{Source: src, Notes: w.notes}, nil
}

// FileName is the file an import of from writes: the configuration's
// directory's name, or the configuration's own where it has none, as a
// .sngl file.
func FileName(from string) string {
	base := path.Base(path.Dir(from))
	if base == "." || base == "/" || base == ".." {
		base = path.Base(from)
		if i := strings.IndexByte(base, '.'); i > 0 {
			base = base[:i]
		}
	}
	return base + ".sngl"
}

type writer struct {
	pkg   analyze.Package
	from  string
	b     strings.Builder
	notes []string
	taken map[string]bool
	// enums are the SNGL names of the catalog's enums, by schema-qualified
	// and bare name.
	enums map[string]string
	// models are the SNGL names of the tables' structs, by table.
	models map[string]model
	time   bool
}

type model struct {
	name   string
	fields []field
}

// field is a column or a parameter as SNGL holds it.
type field struct {
	name string
	// wire is the column's name, which a row is keyed by.
	wire string
	typ  string
	// kind is what the runtime reads a value stored as something else as.
	kind string
}

func newWriter(p analyze.Package, from string) *writer {
	w := &writer{pkg: p, from: from, taken: map[string]bool{}, enums: map[string]string{}, models: map[string]model{}}
	for _, r := range reserved {
		w.taken[strings.ToLower(r)] = true
	}
	return w
}

func (w *writer) note(format string, args ...any) {
	w.notes = append(w.notes, fmt.Sprintf(format, args...))
}

func (w *writer) line(format string, args ...any) {
	fmt.Fprintf(&w.b, format, args...)
	w.b.WriteByte('\n')
}

func (w *writer) engine() string { return "sql.Engine." + w.pkg.Engine }

func (w *writer) write() (string, error) {
	// The declarations first: what they spell decides the imports.
	w.catalog()
	w.queries()
	body := w.b.String()
	w.b.Reset()

	w.line("// Code generated by the sql: scheme from %s. DO NOT EDIT.", w.from)
	w.line("")
	w.line(`import async "sngl:async"`)
	w.line(`import json "sngl:json"`)
	w.line(`import sql "sngl:sql"`)
	if w.time {
		w.line(`import time "sngl:time"`)
	}
	w.line("")
	switch w.pkg.Engine {
	case "sqlite":
		w.line("// The database the queries run against: a file's path, or `:memory:` for")
		w.line("// one held while the program runs. Provide another around the subtree, or")
		w.line("// the test, that queries.")
		w.line(`context #source(":memory:")`)
	case "postgresql":
		w.line("// The database the queries run against, a connection URL,")
		w.line("// `postgres://user:password@host:5432/name`: provide it around the subtree,")
		w.line("// or the test, that queries.")
		w.line(`context #source("")`)
	case "mysql":
		w.line("// The database the queries run against, a DSN,")
		w.line("// `user:password@tcp(host:3306)/name`: provide it around the subtree, or the")
		w.line("// test, that queries.")
		w.line(`context #source("")`)
	}
	w.line("")
	w.line("// The schema's statements, in the order sqlc reads them: what `create` runs.")
	w.line("const schema list[string] = [")
	for _, f := range w.pkg.SchemaFiles {
		for _, s := range split(f.Contents) {
			w.line("%s,", quote(s))
		}
	}
	w.line("]")
	w.line("")
	w.line("// Creates the schema in the database at source, running each of schema's")
	w.line("// statements in order, and answers how many ran: the failure of the first")
	w.line("// that failed, and none after it run.")
	w.line("func create(life async.Lifetime = async.forever) async.Result[int] {")
	w.line("for var text = schema {")
	w.line("match var r = sql.exec(engine=%s, source=source, text=text, life=life) {", w.engine())
	w.line("failed { return {failed=sql.classify(r)} }")
	w.line("ok {}")
	w.line("}")
	w.line("}")
	w.line("return {ok=schema.length()}")
	w.line("}")
	w.line("")
	w.line("// The rows a query answered, decoded, or why there are none.")
	w.line("func _rows[T any](r async.Result[string]) async.Result[list[T]] {")
	w.line("match var got = r {")
	w.line("failed { return {failed=sql.classify(got)} }")
	w.line("ok { return json.decode[list[T]](got) }")
	w.line("}")
	w.line("}")
	w.line("")
	w.line("// The first of rows, or null where there is none.")
	w.line("func _one[T any](rows async.Result[list[T]]) async.Result[option[T]] {")
	w.line("match var got = rows {")
	w.line("failed { return {failed=got} }")
	w.line("ok {")
	w.line("if got.length() == 0 {")
	w.line("return {ok=null}")
	w.line("}")
	w.line("return {ok=got[0]}")
	w.line("}")
	w.line("}")
	w.line("}")
	w.b.WriteString(body)
	doc, err := parser.Parse("sql.sngl", []byte(w.b.String()))
	if err != nil {
		return "", fmt.Errorf("sql: the package written does not parse: %w", err)
	}
	return parser.Format(doc), nil
}

// catalog writes an enum per enum and a struct per table of the schema.
func (w *writer) catalog() {
	cat := w.pkg.Request.GetCatalog()
	for _, s := range cat.GetSchemas() {
		if internalSchema(s.GetName()) {
			continue
		}
		for _, e := range s.GetEnums() {
			w.enum(cat.GetDefaultSchema(), s.GetName(), e)
		}
	}
	for _, s := range cat.GetSchemas() {
		if internalSchema(s.GetName()) {
			continue
		}
		for _, t := range s.GetTables() {
			w.table(cat.GetDefaultSchema(), s.GetName(), t)
		}
	}
}

func internalSchema(name string) bool {
	return name == "pg_catalog" || name == "information_schema" || name == "pg_temp"
}

func qualified(def, schema, name string) string {
	if schema == "" || schema == def {
		return name
	}
	return schema + "_" + name
}

func (w *writer) enum(def, schema string, e *analyze.Enum) {
	want := capital(camel(qualified(def, schema, e.GetName())))
	name := w.claim(want, func(to string) { w.note("enum %s is declared as %s: %s is taken or reserved", e.GetName(), to, want) })
	w.enums[e.GetName()] = name
	w.enums[schema+"."+e.GetName()] = name
	members := map[string]bool{}
	w.line("")
	if c := e.GetComment(); c != "" {
		w.comment(c)
	}
	w.line("enum %s {", name)
	for _, v := range e.GetVals() {
		m := memberName(v)
		for i := 2; members[strings.ToLower(m)]; i++ {
			m = memberName(v) + strconv.Itoa(i)
		}
		members[strings.ToLower(m)] = true
		if m != v {
			w.line("#[json.identifier(%s)]", quote(v))
		}
		w.line("%s", m)
	}
	w.line("}")
}

func memberName(v string) string {
	m := camel(v)
	if m == "" {
		m = "empty"
	}
	m = strings.ToLower(m[:1]) + m[1:]
	if parser.LookupIdent(m) != parser.IDENT {
		m += "_"
	}
	return m
}

func (w *writer) table(def, schema string, t *analyze.Table) {
	tname := t.GetRel().GetName()
	want := capital(camel(analyze.Singular(qualified(def, schema, tname))))
	var fields []field
	taken := map[string]bool{}
	for _, c := range t.GetColumns() {
		typ, kind, why := w.columnType(c)
		if why != "" {
			w.note("column %s of %s is left out of its struct: %s", c.GetName(), tname, why)
			fields = nil
			break
		}
		fields = append(fields, field{name: fieldName(taken, c.GetName(), func(to string) {
			w.note("column %s of %s is the field %s", c.GetName(), tname, to)
		}), wire: c.GetName(), typ: typ, kind: kind})
	}
	if len(fields) == 0 {
		return
	}
	name := w.claim(want, func(to string) { w.note("table %s's struct is %s: %s is taken or reserved", tname, to, want) })
	m := model{name: name, fields: fields}
	w.models[schema+"."+tname] = m
	if schema == def || schema == "" {
		w.models[tname] = m
	}
	w.line("")
	if c := t.GetComment(); c != "" {
		w.comment(c)
	} else {
		w.line("// A row of %s.", tname)
	}
	w.struct_(name, fields)
}

func (w *writer) struct_(name string, fields []field) {
	w.line("struct %s {", name)
	for _, f := range fields {
		w.line("%s %s", f.name, f.typ)
	}
	w.line("}")
}

// query is a query as the package writes it.
type query struct {
	q      *analyze.Query
	fn     string
	adapt  string
	params []field
	// row is the struct a row decodes as; scalar is the field holding the
	// one column of a query answering one.
	row    string
	scalar string
	cols   []field
}

func (w *writer) queries() {
	var qs []*query
	for _, q := range w.pkg.Request.GetQueries() {
		if out := w.query(q); out != nil {
			qs = append(qs, out)
		}
	}
	for _, q := range qs {
		w.function(q)
	}
	for _, q := range qs {
		if q.adapt != "" {
			w.adapter(q)
		}
	}
}

func (w *writer) query(q *analyze.Query) *query {
	switch q.GetCmd() {
	case ":one", ":many", ":exec", ":execrows", ":execlastid", ":execresult":
	default:
		w.note("query %s is left out: %s is not one sngl:sql runs", q.GetName(), q.GetCmd())
		return nil
	}
	out := &query{q: q}
	ptaken := map[string]bool{"life": true}
	for _, p := range q.GetParams() {
		c := p.GetColumn()
		if c.GetIsSqlcSlice() {
			w.note("query %s is left out: its parameter %s is a sqlc.slice", q.GetName(), c.GetName())
			return nil
		}
		typ, kind, why := w.columnType(c)
		if why != "" {
			w.note("query %s is left out: its parameter %s %s", q.GetName(), paramLabel(p), why)
			return nil
		}
		want := c.GetName()
		if want == "" {
			want = "arg" + strconv.Itoa(int(p.GetNumber()))
		}
		name := fieldName(ptaken, want, func(to string) {
			w.note("query %s's parameter %s is %s", q.GetName(), paramLabel(p), to)
		})
		out.params = append(out.params, field{name: name, wire: want, typ: typ, kind: kind})
	}
	base := camel(q.GetName())
	base = strings.ToLower(base[:1]) + base[1:]
	reads := q.GetCmd() == ":one" || q.GetCmd() == ":many"
	if reads {
		if len(q.GetColumns()) == 0 {
			w.note("query %s is left out: it answers no columns", q.GetName())
			return nil
		}
		ctaken := map[string]bool{}
		for _, c := range q.GetColumns() {
			typ, kind, why := w.columnType(c)
			if why != "" {
				w.note("query %s is left out: its column %s %s", q.GetName(), c.GetName(), why)
				return nil
			}
			out.cols = append(out.cols, field{name: fieldName(ctaken, c.GetName(), func(to string) {
				w.note("query %s's column %s is the field %s", q.GetName(), c.GetName(), to)
			}), wire: c.GetName(), typ: typ, kind: kind})
		}
	}
	out.fn = w.claim(base+"Call", func(to string) { w.note("query %s is declared as %s: the name is taken", q.GetName(), to) })
	if !reads {
		return out
	}
	if m, ok := w.modelOf(q); ok {
		out.row = m.name
		out.cols = m.fields
	} else if len(out.cols) == 1 {
		out.row = w.claim(capital(base)+"Row", func(to string) { w.note("query %s's row is %s: the name is taken", q.GetName(), to) })
		out.scalar = out.cols[0].name
	} else {
		out.row = w.claim(capital(base)+"Row", func(to string) { w.note("query %s's row is %s: the name is taken", q.GetName(), to) })
	}
	if !onlyReads(q.GetText()) {
		return out
	}
	for _, p := range out.params {
		if slices.Contains(reservedProps, p.name) {
			w.note("query %s has no component: its parameter %s is named as a component's own prop", q.GetName(), p.name)
			return out
		}
		// A query's key is compared on every target, and each holds its own
		// date and time.
		if strings.Contains(p.typ, "time.date") || strings.Contains(p.typ, "time.time") {
			w.note("query %s has no component: its parameter %s is a %s, and a query's key is compared", q.GetName(), p.name, p.typ)
			return out
		}
	}
	out.adapt = w.claim(base, func(to string) { w.note("query %s's component is %s: the name is taken", q.GetName(), to) })
	return out
}

func paramLabel(p *analyze.Parameter) string {
	if n := p.GetColumn().GetName(); n != "" {
		return n
	}
	return "$" + strconv.Itoa(int(p.GetNumber()))
}

// modelOf is the table's struct a query's row is, where it answers every
// column of one table and nothing else, in the table's order: `SELECT *`.
func (w *writer) modelOf(q *analyze.Query) (model, bool) {
	cols := q.GetColumns()
	if len(cols) == 0 || cols[0].GetTable() == nil {
		return model{}, false
	}
	t := cols[0].GetTable()
	m, ok := w.models[t.GetSchema()+"."+t.GetName()]
	if !ok {
		m, ok = w.models[t.GetName()]
	}
	if !ok || len(m.fields) != len(cols) {
		return model{}, false
	}
	for i, c := range cols {
		ct := c.GetTable()
		if ct == nil || ct.GetName() != t.GetName() || c.GetName() != m.fields[i].wire {
			return model{}, false
		}
		typ, _, _ := w.columnType(c)
		if typ != m.fields[i].typ {
			return model{}, false
		}
	}
	return m, true
}

func (w *writer) signature(q *query) string {
	var ps []string
	for _, p := range q.params {
		ps = append(ps, p.name+" "+p.typ)
	}
	ps = append(ps, "life async.Lifetime = async.forever")
	return strings.Join(ps, ", ")
}

func (w *writer) args(q *query) string {
	if len(q.params) == 0 {
		return `"[]"`
	}
	var parts []string
	for _, p := range q.params {
		parts = append(parts, "{json.encode("+p.name+")}")
	}
	return `"[` + strings.Join(parts, ",") + `]"`
}

func (w *writer) result(q *query) string {
	elem := q.row
	if q.scalar != "" {
		elem = q.cols[0].typ
	}
	switch q.q.GetCmd() {
	case ":one":
		return "option[" + elem + "]"
	case ":many":
		return "list[" + elem + "]"
	case ":execrows", ":execlastid":
		return "int64"
	}
	return "sql.Executed"
}

func (w *writer) function(q *query) {
	if q.row != "" && !strings.Contains(q.row, ".") {
		if _, isModel := w.modelOf(q.q); !isModel {
			w.line("")
			if q.scalar == "" {
				w.line("// A row %s answers.", q.fn)
			}
			w.struct_(q.row, q.cols)
		}
	}
	w.line("")
	for _, c := range q.q.GetComments() {
		w.line("//%s", strings.TrimRight(c, " "))
	}
	if len(q.q.GetComments()) > 0 {
		w.line("//")
	}
	w.line("// %s", strings.Join(strings.Fields(q.q.GetText()), " "))
	w.line("func %s(%s) async.Result[%s] {", q.fn, w.signature(q), w.result(q))
	call := fmt.Sprintf("engine=%s, source=source, text=%s, args=%s", w.engine(), quote(q.q.GetText()), w.args(q))
	var argKinds []string
	anyArgKind := false
	for _, p := range q.params {
		argKinds = append(argKinds, quote(p.kind))
		anyArgKind = anyArgKind || p.kind != ""
	}
	if anyArgKind {
		call += ", argKinds=[" + strings.Join(argKinds, ", ") + "]"
	}
	switch q.q.GetCmd() {
	case ":one", ":many":
		var names, kinds []string
		anyKind := false
		for _, c := range q.cols {
			names = append(names, quote(c.name))
			kinds = append(kinds, quote(c.kind))
			anyKind = anyKind || c.kind != ""
		}
		call += ", columns=[" + strings.Join(names, ", ") + "]"
		if anyKind {
			call += ", kinds=[" + strings.Join(kinds, ", ") + "]"
		}
		rows := fmt.Sprintf("_rows[%s](sql.query(%s, life=life))", q.row, call)
		switch {
		case q.q.GetCmd() == ":one" && q.scalar == "":
			w.line("return _one(%s)", rows)
		case q.q.GetCmd() == ":one":
			w.line("match var r = _one(%s) {", rows)
			w.line("failed { return {failed=r} }")
			w.line("ok {")
			w.line("if r == null {")
			w.line("return {ok=null}")
			w.line("}")
			w.line("return {ok=r.%s}", q.scalar)
			w.line("}")
			w.line("}")
		case q.scalar == "":
			w.line("return %s", rows)
		default:
			w.line("match var r = %s {", rows)
			w.line("failed { return {failed=r} }")
			w.line("ok {")
			w.line("var out list[%s] = []", q.cols[0].typ)
			w.line("for var row = r {")
			w.line("out.push(row.%s)", q.scalar)
			w.line("}")
			w.line("return {ok=out}")
			w.line("}")
			w.line("}")
		}
	default:
		w.line("match var r = sql.exec(%s, life=life) {", call)
		w.line("failed { return {failed=sql.classify(r)} }")
		switch q.q.GetCmd() {
		case ":execrows":
			w.line("ok { return {ok=r.rows} }")
		case ":execlastid":
			w.line("ok { return {ok=r.lastId} }")
		default:
			w.line("ok { return {ok=r} }")
		}
		w.line("}")
	}
	w.line("}")
}

// adapter is a read's component over async.query.
func (w *writer) adapter(q *query) {
	var names []string
	for _, p := range q.params {
		names = append(names, p.name)
	}
	head := "// " + q.q.GetName()
	if len(names) > 0 {
		head += ", asked again whenever " + strings.Join(names, " or ") + " changes"
	}
	result := w.result(q)
	// A `:one` answers an option, and its component holds that option as its
	// value rather than an option of one: null until an answer, and null
	// where the answer was no row, which `pending` tells apart.
	one := q.q.GetCmd() == ":one"
	value := "option[" + result + "]"
	if one {
		value = result
	}
	w.line("")
	w.line("%s.", head)
	w.line("component %s[F any](", q.adapt)
	for _, p := range q.params {
		w.line("%s %s,", p.name, p.typ)
	}
	w.line(":value %s = null,", value)
	w.line(":pending bool = false,")
	w.line(":failure option[async.Failure] = null,")
	w.line("@load(value %s),", result)
	w.line("@fail(f async.Failure),")
	w.line(") F {")
	w.line("// Asks again without a change of key: a retry.")
	w.line("func refresh() {")
	w.line("query.refresh()")
	w.line("}")
	w.line("")
	w.line("async.query #query(")
	switch len(q.params) {
	case 0:
		w.line("on=0,")
		w.line("fetch=func(k int, life async.Lifetime) => %s(life=life),", q.fn)
	case 1:
		w.line("on=%s,", q.params[0].name)
		w.line("fetch=%s,", q.fn)
	default:
		var on, fields, passed []string
		for _, p := range q.params {
			on = append(on, p.name+"="+p.name)
			fields = append(fields, p.name+" "+p.typ)
			passed = append(passed, p.name+"=k."+p.name)
		}
		w.line("on={%s},", strings.Join(on, ", "))
		w.line("fetch=func(k struct { %s }, life async.Lifetime) => %s(%s, life=life),", strings.Join(fields, "; "), q.fn, strings.Join(passed, ", "))
	}
	if !one {
		w.line(":value=value,")
	}
	w.line(":pending=pending,")
	w.line(":failure=failure,")
	if one {
		w.line("@load(got) {")
		w.line("value = got")
		w.line("load(got)")
		w.line("},")
	} else {
		w.line("@load { load() },")
	}
	w.line("@fail { fail() },")
	w.line(")")
	w.line("}")
}

func (w *writer) comment(text string) {
	for l := range strings.SplitSeq(strings.TrimSpace(text), "\n") {
		w.line("// %s", strings.TrimRight(l, " "))
	}
}

// columnType is the SNGL type a column or a parameter holds, what the
// runtime reads it as where the database stores it as something else, and,
// where it has no SNGL type, why.
func (w *writer) columnType(c *analyze.Column) (typ, kind, why string) {
	base, kind, why := w.scalar(c)
	if why != "" {
		return "", "", why
	}
	if c.GetIsArray() || c.GetArrayDims() > 0 {
		return "", "", "is an array, which sngl:sql does not read yet"
	}
	if !c.GetNotNull() {
		return "option[" + base + "]", kind, ""
	}
	return base, kind, ""
}

func (w *writer) scalar(c *analyze.Column) (typ, kind, why string) {
	t := c.GetType()
	name := strings.ToLower(t.GetName())
	if t.GetSchema() != "" && t.GetSchema() != "pg_catalog" {
		if e, ok := w.enums[t.GetSchema()+"."+t.GetName()]; ok {
			return e, "", ""
		}
	}
	if e, ok := w.enums[t.GetName()]; ok {
		return e, "", ""
	}
	name = strings.TrimPrefix(name, "pg_catalog.")
	switch w.pkg.Engine {
	case "postgresql":
		return postgresType(name, w)
	case "mysql":
		return mysqlType(name, c, w)
	}
	return sqliteType(name, w)
}

func postgresType(name string, w *writer) (string, string, string) {
	switch name {
	case "int2", "smallint", "int4", "integer", "int", "serial", "serial4", "smallserial", "serial2":
		return "int", "", ""
	case "int8", "bigint", "bigserial", "serial8", "oid":
		return "int64", "", ""
	case "float4", "real", "float8", "float", "double precision":
		return "float", "", ""
	case "bool", "boolean":
		return "bool", "", ""
	case "text", "varchar", "character varying", "char", "character", "bpchar", "citext", "name", "uuid",
		"inet", "cidr", "macaddr", "macaddr8", "json", "jsonb", "xml",
		"time", "timetz", "time without time zone", "time with time zone", "interval", "ltree", "tsvector":
		return "string", "", ""
	case "bytea":
		return "string", "", ""
	case "date":
		w.time = true
		return "time.date", "date", ""
	case "timestamp", "timestamptz", "timestamp without time zone", "timestamp with time zone":
		w.time = true
		return "time.datetime", "datetime", ""
	case "numeric", "decimal", "money":
		return "", "", exactDecimal(name, "::text", "::float8")
	case "void":
		return "", "", "has no value"
	}
	return "", "", "is of type " + name + ", which sngl:sql has no type for"
}

// exactDecimal says why an exact decimal is left out: SNGL has no number to
// hold every digit of one, and a float or a string would each change what
// the column means without the program saying so. The query can say it.
func exactDecimal(name, text, float string) string {
	return "is a " + name + ", which SNGL has no exact number for yet: cast it in the query, `" + text + "` or, where rounding is fine, `" + float + "`"
}

func mysqlType(name string, c *analyze.Column, w *writer) (string, string, string) {
	switch name {
	case "tinyint":
		if c.GetLength() == 1 {
			return "bool", "bool", ""
		}
		return "int", "", ""
	case "bool", "boolean":
		return "bool", "bool", ""
	case "smallint", "mediumint", "int", "integer", "year":
		return "int", "", ""
	case "bigint":
		return "int64", "", ""
	case "float", "double", "double precision", "real":
		return "float", "", ""
	case "decimal", "dec", "numeric", "fixed":
		return "", "", exactDecimal(name, "CAST(… AS CHAR)", "CAST(… AS DOUBLE)")
	case "char", "varchar", "var_string", "string", "tinytext", "text", "mediumtext", "longtext", "json", "enum", "set", "time",
		"binary", "varbinary", "tinyblob", "blob", "mediumblob", "longblob":
		return "string", "", ""
	case "date":
		w.time = true
		return "time.date", "date", ""
	case "datetime", "timestamp":
		w.time = true
		return "time.datetime", "datetime", ""
	}
	return "", "", "is of type " + name + ", which sngl:sql has no type for"
}

// sqliteType reads a declared type by SQLite's own affinity rules, as sqlc's
// SQLite engine does, with the names a column declares a date or a boolean
// by.
func sqliteType(name string, w *writer) (string, string, string) {
	switch {
	case name == "boolean" || name == "bool":
		return "bool", "bool", ""
	case name == "date":
		w.time = true
		return "time.date", "date", ""
	case name == "datetime" || name == "timestamp":
		w.time = true
		return "time.datetime", "datetime", ""
	case strings.Contains(name, "int"):
		return "int64", "", ""
	case strings.Contains(name, "char") || strings.Contains(name, "clob") || strings.Contains(name, "text"):
		return "string", "", ""
	case strings.Contains(name, "blob"):
		return "string", "", ""
	case strings.Contains(name, "real") || strings.Contains(name, "floa") || strings.Contains(name, "doub"):
		return "float", "", ""
	case name == "numeric" || strings.HasPrefix(name, "decimal"):
		return "float", "", ""
	case name == "" || name == "any":
		return "", "", "has no type sqlc could tell: cast it, `CAST(x AS TEXT)`"
	}
	return "", "", "is of type " + name + ", which sngl:sql has no type for"
}

// claim takes want, or the first free name made from it. Two names
// differing only in case are one: Go exports both as one identifier.
func (w *writer) claim(want string, renamed func(to string)) string {
	got := want
	if parser.LookupIdent(got) != parser.IDENT {
		got += "_"
	}
	for i := 2; w.taken[strings.ToLower(got)]; i++ {
		got = want + strconv.Itoa(i)
	}
	w.taken[strings.ToLower(got)] = true
	if got != want && renamed != nil {
		renamed(got)
	}
	return got
}

// fieldName names a struct's field or a function's parameter in its own
// namespace, taken.
func fieldName(taken map[string]bool, want string, renamed func(to string)) string {
	base := camel(want)
	if base == "" {
		base = "column"
	}
	base = strings.ToLower(base[:1]) + base[1:]
	got := base
	if parser.LookupIdent(got) != parser.IDENT {
		got += "_"
	}
	for i := 2; taken[strings.ToLower(got)]; i++ {
		got = base + strconv.Itoa(i)
	}
	taken[strings.ToLower(got)] = true
	if got != base && renamed != nil {
		renamed(got)
	}
	return got
}

// camel is a SQL name as a SNGL identifier: each run after an underscore or
// any other mark starts upper case, `author_name` → `authorName`; the first
// keeps its case.
func camel(s string) string {
	var b strings.Builder
	upper := false
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) || r > unicode.MaxASCII {
			upper = b.Len() > 0
			continue
		}
		if b.Len() == 0 && unicode.IsDigit(r) {
			b.WriteByte('n')
		}
		if upper {
			r = unicode.ToUpper(r)
			upper = false
		}
		b.WriteRune(r)
	}
	return b.String()
}

func capital(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func quote(s string) string { return `"` + ast.EscapeString(s, ast.StyleDouble) + `"` }

// onlyReads reports whether a statement only reads, which a component may ask
// again whenever its parameters change: a `:one` is as often an INSERT …
// RETURNING, which must run once, when it is called.
func onlyReads(text string) bool {
	words := strings.FieldsFunc(strings.ToUpper(skipComments(text)), func(r rune) bool {
		return !unicode.IsLetter(r) && r != '_'
	})
	if len(words) == 0 {
		return false
	}
	switch words[0] {
	case "SELECT", "VALUES", "TABLE", "SHOW", "EXPLAIN":
		return true
	case "WITH":
		// A data-modifying CTE writes whatever its last statement does.
		for _, w := range words {
			switch w {
			case "INSERT", "UPDATE", "DELETE", "MERGE":
				return false
			}
		}
		return true
	}
	return false
}
