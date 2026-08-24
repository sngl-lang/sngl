package golang

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/ir"
)

const testpkgPath = "git.duckfam.us/jonathan/sngl/codegen/scheme/golang/testdata/testpkg"

// withNativeType registers fn for the duration of the test. The registry is
// process-global and RegisterType refuses a duplicate, so a test that wants a
// mapping has to take it back afterwards.
func withNativeType(t *testing.T, qualifiedName string, fn func() *ir.Type) {
	t.Helper()
	if _, dup := nativeTypes[qualifiedName]; dup {
		t.Fatalf("%s is already registered", qualifiedName)
	}
	nativeTypes[qualifiedName] = fn
	t.Cleanup(func() { delete(nativeTypes, qualifiedName) })
}

// The registry is consulted before the switch on Underlying(). Priority's
// underlying type is a basic int, which the switch would answer on its own, so
// a lookup placed after it could never fire — and time.Duration, the type that
// motivated the registry, is exactly that shape.
func TestRegisteredTypeBeatsUnderlying(t *testing.T) {
	imp := &GoImporter{}

	ni, err := imp.Resolve("go://"+testpkgPath, ".")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := returnKind(t, ni, "Rank"); got != ir.TypeInt {
		t.Fatalf("unregistered Priority imported as %v, want int — the test cannot show the registry winning", got)
	}

	// bool is nothing a named int could import as by any other route, so
	// seeing it is the registry and only the registry.
	calls := 0
	withNativeType(t, testpkgPath+".Priority", func() *ir.Type {
		calls++
		return ir.TypBool
	})
	if calls != 0 {
		t.Error("the type function ran at registration; it must not, since the stdlib may not be loaded yet")
	}

	ni, err = imp.Resolve("go://"+testpkgPath, ".")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := returnKind(t, ni, "Rank"); got != ir.TypeBool {
		t.Errorf("registered Priority imported as %v, want bool", got)
	}
	if calls == 0 {
		t.Error("the type function was never called")
	}
}

// returnKind is the kind of a named imported function's return type.
func returnKind(t *testing.T, ni *ir.NativeImport, name string) ir.TypeKind {
	t.Helper()
	for _, f := range ni.Funcs {
		if f.Name == name {
			if f.Return == nil {
				t.Fatalf("%s has no return type", name)
			}
			return f.Return.Kind
		}
	}
	t.Fatalf("no imported func %s", name)
	return ir.TypeInvalid
}

// time.Time's mapping fires too. Its underlying type is a struct from another
// package, which the importer would otherwise call unusable — so this pins the
// half of the pair that moved into the registry alongside Duration.
func TestRegisteredStructTypeIsUsable(t *testing.T) {
	ni, err := (&GoImporter{}).Resolve("go://"+testpkgPath, ".")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	for _, f := range ni.Funcs {
		if f.Name != "Stamped" {
			continue
		}
		if f.Unusable != "" {
			t.Errorf("a func returning time.Time is unusable: %s", f.Unusable)
		}
		return
	}
	t.Fatal("no imported func Stamped")
}

// Two mappings for one Go type mean two callers disagree about what it is, and
// the one that lost would be decided by init order — so the second is a
// programming error, not the winner.
func TestRegisterTypeRejectsDuplicate(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("registering time.Duration twice was accepted")
		}
		if msg, _ := r.(string); !strings.Contains(msg, "time.Duration") {
			t.Errorf("panic does not name the type: %v", r)
		}
	}()
	RegisterType("time.Duration", ir.DurationType)
}

// The two mappings the compiler ships are registered by stdtypes.go, whose
// encode-side mirror is pkg/go/consteval/stdtypes.go.
func TestStdTypesAreRegistered(t *testing.T) {
	for _, key := range []string{"time.Duration", "time.Time"} {
		if _, ok := nativeTypes[key]; !ok {
			t.Errorf("%s has no registered mapping", key)
		}
	}
}
