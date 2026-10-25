//go:build !js

package gtk4rt

/*
#cgo pkg-config: gtk4
#cgo LDFLAGS: -ldl
#include <gtk/gtk.h>
#include <dlfcn.h>
#include <stdlib.h>

// A GTK type is registered the first time its get_type runs, which no call
// here makes by name.
static GType sngl_rt_type(const char *name) {
    GType t = g_type_from_name(name);
    if (t == 0) {
        gtk_test_register_all_types();
        t = g_type_from_name(name);
    }
    return t;
}
static void *sngl_rt_new(GType t) { return g_object_new(t, NULL); }

static void sngl_rt_set(void *obj, const char *name, GValue *src) {
    GObject *o = G_OBJECT(obj);
    GParamSpec *ps = g_object_class_find_property(G_OBJECT_GET_CLASS(o), name);
    if (ps == NULL) {
        g_warning("sngl: %s has no property \"%s\"", G_OBJECT_TYPE_NAME(o), name);
    } else if (g_value_type_compatible(G_VALUE_TYPE(src), ps->value_type)) {
        g_object_set_property(o, name, src);
    } else {
        GValue dst = G_VALUE_INIT;
        g_value_init(&dst, ps->value_type);
        if (g_value_transform(src, &dst)) {
            g_object_set_property(o, name, &dst);
        } else {
            g_warning("sngl: cannot convert %s to %s for %s.%s", G_VALUE_TYPE_NAME(src), g_type_name(ps->value_type), G_OBJECT_TYPE_NAME(o), name);
        }
        g_value_unset(&dst);
    }
    g_value_unset(src);
}
static void sngl_rt_set_string(void *obj, const char *name, const char *v) {
    GValue src = G_VALUE_INIT;
    g_value_init(&src, G_TYPE_STRING);
    g_value_set_string(&src, v);
    sngl_rt_set(obj, name, &src);
}
static void sngl_rt_set_bool(void *obj, const char *name, int v) {
    GValue src = G_VALUE_INIT;
    g_value_init(&src, G_TYPE_BOOLEAN);
    g_value_set_boolean(&src, v ? TRUE : FALSE);
    sngl_rt_set(obj, name, &src);
}
static void sngl_rt_set_int(void *obj, const char *name, int v) {
    GValue src = G_VALUE_INIT;
    g_value_init(&src, G_TYPE_INT);
    g_value_set_int(&src, v);
    sngl_rt_set(obj, name, &src);
}
static void sngl_rt_set_double(void *obj, const char *name, double v) {
    GValue src = G_VALUE_INIT;
    g_value_init(&src, G_TYPE_DOUBLE);
    g_value_set_double(&src, v);
    sngl_rt_set(obj, name, &src);
}

static void *sngl_rt_symbol(const char *name) {
    static void *self;
    if (self == NULL) {
        self = dlopen(NULL, RTLD_LAZY);
    }
    return dlsym(self, name);
}
// Every child adder and remover GIR names takes the container and the child
// and nothing else (gir.ChildAdder); what one returns is ignored.
static void sngl_rt_call_pair(void *fn, void *a, void *b) {
    ((void (*)(void *, void *))fn)(a, b);
}
*/
import "C"

import (
	"fmt"
	"sync"
	"unsafe"
)

// The names handed to these are the literals a generated file spells, so each
// is made into C once and kept for the process.
var (
	cNamesMu sync.Mutex
	cNames   = map[string]*C.char{}
	types    = map[string]C.GType{}
	symbols  = map[string]unsafe.Pointer{}
)

func cName(s string) *C.char {
	cNamesMu.Lock()
	defer cNamesMu.Unlock()
	c, ok := cNames[s]
	if !ok {
		c = C.CString(s)
		cNames[s] = c
	}
	return c
}

// New makes a widget of the GTK class named typeName, as its constructor
// would with no arguments; its props are written once it returns.
func New(typeName string) Handle {
	cNamesMu.Lock()
	t, ok := types[typeName]
	cNamesMu.Unlock()
	if !ok {
		t = C.sngl_rt_type(cName(typeName))
		if t == 0 {
			panic(fmt.Sprintf("gtk4rt: GTK registers no type %q", typeName))
		}
		cNamesMu.Lock()
		types[typeName] = t
		cNamesMu.Unlock()
	}
	return Handle(C.sngl_rt_new(t))
}

// SetString, SetBool, SetInt and SetFloat write the property GTK knows as prop,
// converting the value to the type its GParamSpec declares: an enumeration's
// member is written as its value through SetInt.
func SetString(w Handle, prop, v string) {
	c, free := cstr(v)
	defer free()
	C.sngl_rt_set_string(p(w), cName(prop), c)
}

func SetBool(w Handle, prop string, v bool) {
	C.sngl_rt_set_bool(p(w), cName(prop), C.int(gbool(v)))
}

func SetInt(w Handle, prop string, v int) {
	C.sngl_rt_set_int(p(w), cName(prop), C.int(v))
}

func SetFloat(w Handle, prop string, v float64) {
	C.sngl_rt_set_double(p(w), cName(prop), C.double(v))
}

// Notify reports prop changed on w, as a write of it would; g_signal_emit_by_name
// cannot be handed the GParamSpec a notification carries.
func Notify(w Handle, prop string) {
	C.g_object_notify((*C.GObject)(p(w)), cName(prop))
}

// ChildCall calls the GTK function named fn with a container and a child: the
// adder or remover GIR names for a class gtk4rt has no wrapper of.
func ChildCall(fn string, parent, child Handle) {
	cNamesMu.Lock()
	sym, ok := symbols[fn]
	cNamesMu.Unlock()
	if !ok {
		sym = C.sngl_rt_symbol(cName(fn))
		if sym == nil {
			panic(fmt.Sprintf("gtk4rt: no GTK function %q", fn))
		}
		cNamesMu.Lock()
		symbols[fn] = sym
		cNamesMu.Unlock()
	}
	C.sngl_rt_call_pair(sym, p(parent), p(child))
}
