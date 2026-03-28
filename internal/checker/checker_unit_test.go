package checker

import (
	"testing"
)

func TestInferLiteralType_Float(t *testing.T) {
	if got := InferLiteralType(3.14); got != Float {
		t.Errorf("expected Float, got %v", got)
	}
}

func TestInferLiteralType_Bool(t *testing.T) {
	if got := InferLiteralType(true); got != Bool {
		t.Errorf("expected Bool, got %v", got)
	}
}

func TestInferLiteralType_Nil(t *testing.T) {
	if got := InferLiteralType(nil); got != Dyn {
		t.Errorf("expected Dyn for nil, got %v", got)
	}
}

func TestInferLiteralType_Unknown(t *testing.T) {
	// A struct value is not a recognized literal type, should return Dyn.
	type custom struct{}
	if got := InferLiteralType(custom{}); got != Dyn {
		t.Errorf("expected Dyn for unknown type, got %v", got)
	}
}

func TestLookupMethod_Found(t *testing.T) {
	c := &checker{
		methods: map[string]map[string]*methodInfo{
			"string": {
				"length": &methodInfo{ReturnType: "int"},
			},
		},
	}
	typ, ok := c.lookupMethod("string", "length")
	if !ok {
		t.Fatal("expected lookupMethod to find string.length")
	}
	if typ != Int {
		t.Errorf("expected Int, got %v", typ)
	}
}

func TestLookupMethod_FoundNoReturnType(t *testing.T) {
	c := &checker{
		methods: map[string]map[string]*methodInfo{
			"string": {
				"custom": &methodInfo{},
			},
		},
	}
	typ, ok := c.lookupMethod("string", "custom")
	if !ok {
		t.Fatal("expected lookupMethod to find string.custom")
	}
	if typ != Dyn {
		t.Errorf("expected Dyn for empty ReturnType, got %v", typ)
	}
}

func TestLookupMethod_NotFound(t *testing.T) {
	c := &checker{
		methods: map[string]map[string]*methodInfo{},
	}
	_, ok := c.lookupMethod("string", "bogus")
	if ok {
		t.Error("expected lookupMethod to return false for unknown method")
	}
}

func TestIsExported_Uppercase(t *testing.T) {
	if !isExported("Foo") {
		t.Error("expected 'Foo' to be exported")
	}
}

func TestIsExported_Lowercase(t *testing.T) {
	if isExported("foo") {
		t.Error("expected 'foo' to not be exported")
	}
}

func TestIsExported_Empty(t *testing.T) {
	if isExported("") {
		t.Error("expected empty string to not be exported")
	}
}
