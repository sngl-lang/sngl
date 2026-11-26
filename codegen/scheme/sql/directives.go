package sql

import (
	"fmt"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Directive is the prefix of a comment line a `.sql` file says how it is
// read with: `-- sngl:schema schema.sql` in a query file, `-- sngl:engine
// postgresql` in a schema file or, to override it, a query file.
const Directive = "-- sngl:"

// directive is one `-- sngl:` line of a file.
type directive struct {
	key, value string
	at         string
}

func directives(text, file string) ([]directive, error) {
	var out []directive
	for n, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, Directive) {
			continue
		}
		at := fmt.Sprintf("%s:%d", file, n+1)
		key, value, _ := strings.Cut(strings.TrimPrefix(line, Directive), " ")
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, fmt.Errorf("%s: sngl:%s names nothing", at, key)
		}
		if key == "engine" && !slices.Contains(engines, value) {
			return nil, fmt.Errorf("%s: sngl:engine %s is none of %s", at, value, strings.Join(engines, ", "))
		}
		out = append(out, directive{key: key, value: value, at: at})
	}
	return out, nil
}

// queryDirectives are what a query file says: the schema it is written
// against, each a file, a directory or a pattern relative to it, and the
// engine where it overrides the schema's.
func queryDirectives(queries, from string) (schema []string, engine string, err error) {
	ds, err := directives(queries, path.Clean(from))
	if err != nil {
		return nil, "", err
	}
	for _, d := range ds {
		switch d.key {
		case "schema":
			schema = append(schema, d.value)
		case "engine":
			engine = d.value
		default:
			return nil, "", fmt.Errorf("%s: sngl:%s is no directive: a query file says sngl:schema, and sngl:engine to override its schema's", d.at, d.key)
		}
	}
	if len(schema) == 0 {
		return nil, "", fmt.Errorf("sql:%s names no schema: say where it is with `%sschema <path>`", from, Directive)
	}
	return schema, engine, nil
}

// schemaEngine is the engine the schema's files name, SQLite where none
// does. Every file naming one names the same.
func schemaEngine(files map[string]string, query string) (string, error) {
	names := make([]string, 0, len(files))
	for name := range files {
		if name != query {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	engine, from := "", ""
	for _, name := range names {
		ds, err := directives(files[name], name)
		if err != nil {
			return "", err
		}
		for _, d := range ds {
			if d.key != "engine" {
				return "", fmt.Errorf("%s: sngl:%s is no directive: a schema file says sngl:engine", d.at, d.key)
			}
			if engine != "" && d.value != engine {
				return "", fmt.Errorf("%s: sngl:engine %s, where %s says %s", d.at, d.value, from, engine)
			}
			engine, from = d.value, d.at
		}
	}
	if engine == "" {
		engine = "sqlite"
	}
	return engine, nil
}

// ConfigFor is the sqlc configuration a query file, its text queries and its
// name name, stands for: its own queries over the schema its `sngl:schema`
// directives name, for engine.
func ConfigFor(queries, name, from, engine string) (string, error) {
	schema, own, err := queryDirectives(queries, from)
	if err != nil {
		return "", err
	}
	if own != "" {
		engine = own
	}
	quoted := make([]string, len(schema))
	for i, s := range schema {
		quoted[i] = strconv.Quote(s)
	}
	return fmt.Sprintf("version: \"2\"\nsql:\n  - engine: %s\n    schema: [%s]\n    queries: [%s]\n",
		engine, strings.Join(quoted, ", "), strconv.Quote(path.Base(name))), nil
}
