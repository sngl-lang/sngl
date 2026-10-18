// Package sngljson is the runtime behind sngl:json and the query sngl:http
// sends, for a program compiled to Go.
//
// The compiler hands each call a Codec it wrote from the SNGL type -- the
// type's shape and the name each field and enum member travels under -- and
// this package reads and writes values of the Go type the program declares
// for it by reflection. The rules are the ones every target keeps, and the
// interpreter, JavaScript's and Kotlin's runtimes say the same things in the
// same words: a key the type does not name is ignored, a missing key is a
// failure unless its field is an option, a value of the wrong kind is a
// failure naming where it was, and writing leaves an absent option's key out.
package sngljson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// Kind is the shape of a value on the wire.
type Kind int

const (
	Bool Kind = iota
	Int
	Float
	String
	Date
	Datetime
	Option
	List
	Map
	Enum
	Struct
)

// Codec describes a value of one SNGL type.
type Codec struct {
	Kind Kind
	// Elem is an option's element, a list's or a map's value.
	Elem *Codec
	// Fields are a struct's, in the order the Go struct declares them.
	Fields []Field
	// Wires and Names are an enum's members: the name each travels under,
	// and the value Go holds for it.
	Wires, Names []string
}

// Field is one field of a struct.
type Field struct {
	Wire  string
	Codec *Codec
}

const (
	dateLayout     = "2006-01-02"
	datetimeLayout = time.RFC3339Nano
)

// Decode reads text as a T, or says what in it did not match.
func Decode[T any](text string, c *Codec) (T, string) {
	var out T
	v, msg := Parse(text)
	if msg != "" {
		return out, msg
	}
	if msg := decode(v, c, reflect.ValueOf(&out).Elem(), "$"); msg != "" {
		return out, msg
	}
	return out, ""
}

// Parse reads JSON text into the generic tree decode walks: nil, bool,
// json.Number, string, []any, map[string]any.
func Parse(text string) (any, string) {
	d := json.NewDecoder(strings.NewReader(text))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, "not JSON"
	}
	if d.More() {
		return nil, "not JSON"
	}
	return v, ""
}

// Want is the failure for a value of the wrong kind at path.
func Want(path string, c *Codec) string {
	return path + ": want " + Describe(c)
}

// Describe names what c reads, for a failure.
func Describe(c *Codec) string {
	switch c.Kind {
	case Bool:
		return "a boolean"
	case Int:
		return "an integer"
	case Float:
		return "a number"
	case String:
		return "a string"
	case Date:
		return "a date"
	case Datetime:
		return "a datetime"
	case Option:
		return Describe(c.Elem)
	case List:
		return "an array"
	case Map, Struct:
		return "an object"
	case Enum:
		return "one of " + strings.Join(c.Wires, ", ")
	}
	return "a value"
}

func decode(v any, c *Codec, out reflect.Value, path string) string {
	if c.Kind == Option {
		if v == nil {
			out.SetZero()
			return ""
		}
		box := reflect.New(out.Type().Elem())
		if msg := decode(v, c.Elem, box.Elem(), path); msg != "" {
			return msg
		}
		out.Set(box)
		return ""
	}
	switch c.Kind {
	case Bool:
		b, ok := v.(bool)
		if !ok {
			return Want(path, c)
		}
		out.SetBool(b)
	case Int:
		n, ok := v.(json.Number)
		if !ok {
			return Want(path, c)
		}
		i, ok := Integer(string(n))
		if !ok {
			return Want(path, c)
		}
		if out.CanInt() {
			if out.OverflowInt(i) {
				return Want(path, c)
			}
			out.SetInt(i)
		} else {
			if i < 0 || out.OverflowUint(uint64(i)) {
				return Want(path, c)
			}
			out.SetUint(uint64(i))
		}
	case Float:
		n, ok := v.(json.Number)
		if !ok {
			return Want(path, c)
		}
		f, err := strconv.ParseFloat(string(n), 64)
		if err != nil {
			return Want(path, c)
		}
		out.SetFloat(f)
	case String:
		s, ok := v.(string)
		if !ok {
			return Want(path, c)
		}
		out.SetString(s)
	case Date, Datetime:
		s, ok := v.(string)
		if !ok {
			return Want(path, c)
		}
		layout := dateLayout
		if c.Kind == Datetime {
			layout = datetimeLayout
		}
		t, err := time.Parse(layout, s)
		if err != nil {
			return Want(path, c)
		}
		out.Set(reflect.ValueOf(t))
	case Enum:
		s, ok := v.(string)
		if !ok {
			return Want(path, c)
		}
		for i, w := range c.Wires {
			if w == s {
				out.SetString(c.Names[i])
				return ""
			}
		}
		return Want(path, c)
	case List:
		xs, ok := v.([]any)
		if !ok {
			return Want(path, c)
		}
		list := reflect.MakeSlice(out.Type(), len(xs), len(xs))
		for i, x := range xs {
			if msg := decode(x, c.Elem, list.Index(i), path+"["+strconv.Itoa(i)+"]"); msg != "" {
				return msg
			}
		}
		out.Set(list)
	case Map:
		obj, ok := v.(map[string]any)
		if !ok {
			return Want(path, c)
		}
		m := reflect.MakeMapWithSize(out.Type(), len(obj))
		for _, k := range sortedKeys(obj) {
			el := reflect.New(out.Type().Elem()).Elem()
			if msg := decode(obj[k], c.Elem, el, path+"."+k); msg != "" {
				return msg
			}
			m.SetMapIndex(reflect.ValueOf(k).Convert(out.Type().Key()), el)
		}
		out.Set(m)
	case Struct:
		obj, ok := v.(map[string]any)
		if !ok {
			return Want(path, c)
		}
		for i, f := range c.Fields {
			x, present := obj[f.Wire]
			if !present && f.Codec.Kind != Option {
				return path + "." + f.Wire + ": missing"
			}
			if msg := decode(x, f.Codec, out.Field(i), path+"."+f.Wire); msg != "" {
				return msg
			}
		}
	}
	return ""
}

// Integer reads a JSON number that has no fractional part as an int64.
func Integer(n string) (int64, bool) {
	if i, err := strconv.ParseInt(n, 10, 64); err == nil {
		return i, true
	}
	f, err := strconv.ParseFloat(n, 64)
	if err != nil || f != math.Trunc(f) || math.Abs(f) > 1<<63-1 {
		return 0, false
	}
	return int64(f), true
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Sorted, so a failure names the first key in order on every target.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// Encode writes v as compact JSON text.
func Encode[T any](v T, c *Codec) string {
	var b bytes.Buffer
	encode(&b, reflect.ValueOf(&v).Elem(), c)
	return b.String()
}

func encode(b *bytes.Buffer, v reflect.Value, c *Codec) {
	switch c.Kind {
	case Option:
		if v.IsNil() {
			b.WriteString("null")
			return
		}
		encode(b, v.Elem(), c.Elem)
	case Bool:
		b.WriteString(strconv.FormatBool(v.Bool()))
	case Int:
		if v.CanInt() {
			b.WriteString(strconv.FormatInt(v.Int(), 10))
		} else {
			b.WriteString(strconv.FormatUint(v.Uint(), 10))
		}
	case Float:
		b.WriteString(Number(v.Float()))
	case String:
		Quote(b, v.String())
	case Date:
		Quote(b, v.Interface().(time.Time).Format(dateLayout))
	case Datetime:
		Quote(b, v.Interface().(time.Time).Format(time.RFC3339))
	case Enum:
		Quote(b, enumWire(c, v.String()))
	case List:
		b.WriteByte('[')
		for i := range v.Len() {
			if i > 0 {
				b.WriteByte(',')
			}
			encode(b, v.Index(i), c.Elem)
		}
		b.WriteByte(']')
	case Map:
		b.WriteByte('{')
		keys := v.MapKeys()
		names := make([]string, len(keys))
		byName := map[string]reflect.Value{}
		for i, k := range keys {
			names[i] = k.String()
			byName[names[i]] = k
		}
		for i := 1; i < len(names); i++ {
			for j := i; j > 0 && names[j] < names[j-1]; j-- {
				names[j], names[j-1] = names[j-1], names[j]
			}
		}
		for i, k := range names {
			if i > 0 {
				b.WriteByte(',')
			}
			Quote(b, k)
			b.WriteByte(':')
			encode(b, v.MapIndex(byName[k]), c.Elem)
		}
		b.WriteByte('}')
	case Struct:
		b.WriteByte('{')
		first := true
		for i, f := range c.Fields {
			fv := v.Field(i)
			if f.Codec.Kind == Option && fv.IsNil() {
				continue
			}
			if !first {
				b.WriteByte(',')
			}
			first = false
			Quote(b, f.Wire)
			b.WriteByte(':')
			encode(b, fv, f.Codec)
		}
		b.WriteByte('}')
	}
}

func enumWire(c *Codec, name string) string {
	for i, n := range c.Names {
		if n == name {
			return c.Wires[i]
		}
	}
	return name
}

// Number writes a float as JSON (and JavaScript) writes one: the shortest
// decimal that reads back as the same value, with no fraction for a whole
// number and an exponent from 1e21 up and below 1e-6.
func Number(f float64) string {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return "null"
	}
	out, _ := json.Marshal(f)
	return string(out)
}

// Quote writes s as a JSON string: `"` and `\` escaped, the control
// characters written as \b \f \n \r \t or \u00XX, and everything else as it
// is -- which is what JavaScript's JSON.stringify writes.
func Quote(b *bytes.Buffer, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

// Query writes a query struct's fields as a URL's query, in the order the
// struct declares them: an absent option is left off, a list repeats its
// key, and anything else is written once. "" for nothing to write.
func Query(q any, c *Codec) string {
	if c == nil || c.Kind != Struct {
		return ""
	}
	v := reflect.ValueOf(q)
	var parts []string
	for i, f := range c.Fields {
		for _, s := range queryValues(v.Field(i), f.Codec) {
			parts = append(parts, Escape(f.Wire)+"="+Escape(s))
		}
	}
	return strings.Join(parts, "&")
}

func queryValues(v reflect.Value, c *Codec) []string {
	switch c.Kind {
	case Option:
		if v.IsNil() {
			return nil
		}
		return queryValues(v.Elem(), c.Elem)
	case List:
		var out []string
		for i := range v.Len() {
			out = append(out, queryValues(v.Index(i), c.Elem)...)
		}
		return out
	case Bool:
		return []string{strconv.FormatBool(v.Bool())}
	case Int:
		if v.CanInt() {
			return []string{strconv.FormatInt(v.Int(), 10)}
		}
		return []string{strconv.FormatUint(v.Uint(), 10)}
	case Float:
		return []string{Number(v.Float())}
	case Date:
		return []string{v.Interface().(time.Time).Format(dateLayout)}
	case Datetime:
		return []string{v.Interface().(time.Time).Format(time.RFC3339)}
	case Enum:
		return []string{enumWire(c, v.String())}
	}
	return []string{v.String()}
}

// WithQuery is url with query added: after a `?`, or an `&` where url
// already has a query.
func WithQuery(url, query string) string {
	if query == "" {
		return url
	}
	if strings.Contains(url, "?") {
		return url + "&" + query
	}
	return url + "?" + query
}

// Escape percent-encodes every byte of s but an ASCII letter, a digit and
// `-`, `.`, `_` and `~`: the unreserved characters, which mean the same
// thing in every part of a URL.
func Escape(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z' || '0' <= c && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&15])
	}
	return b.String()
}
