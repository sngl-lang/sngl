package grpc

import (
	"slices"
	"strings"
	"testing"
)

const edges = `syntax = "proto3";
package edge.v1;

message Outer {
  message Inner {
    string func = 1;
  }
  Inner inner = 1;
  oneof choice {
    string text = 2;
    sint32 number = 3;
    Inner none = 6;
  }
  map<int32, string> byId = 4;
  repeated string list = 5;
}

enum Mood {
  option allow_alias = true;
  MOOD_UNSPECIFIED = 0;
  MOOD_HAPPY = 1;
  MOOD_GLAD = 1;
  MOOD_2X = 2;
}

message Server {
  Mood mood = 1;
  int64 id = 2;
  fixed64 isbn = 3;
  uint32 count = 4;
  int32 small = 5;
}

service Edge {
  rpc Find(Outer) returns (Server) {
    option idempotency_level = NO_SIDE_EFFECTS;
  }
  rpc Chat(stream Server) returns (stream Server);
}
`

func TestEdges(t *testing.T) {
	p, err := Generate(edges, "edge.proto")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"struct OuterInner {",
		"func_ string = \"\"",
		"choice OuterChoice = OuterChoice.none",
		"enum OuterChoice {\n    none\n    #[grpc.field(2)]\n    text   string\n    #[grpc.field(3, sint32)]\n    number int\n    #[grpc.field(6)]\n    none2  OuterInner\n}",
		"list   list[string] = []",
		"struct Server2 {",
		"#[grpc.value(1)]\n    happy\n",
		"#[grpc.value(2)]\n    mood2x\n",
		"func findCall(request Outer",
		"mood  Mood = Mood.unspecified",
		"#[grpc.field(2)]\n    id    int64 = 0",
		"#[grpc.field(3, fixed64)]\n    isbn  uint64 = 0",
		"#[grpc.field(4)]\n    count uint32 = 0",
		"#[grpc.field(5, int32)]\n    small int = 0",
	} {
		if !strings.Contains(p.Source, want) {
			t.Errorf("source has no %q:\n%s", want, p.Source)
		}
	}
	for _, deny := range []string{"byId", "glad", "component find", "chatCall"} {
		if strings.Contains(p.Source, deny) {
			t.Errorf("source has %q, which the import leaves out:\n%s", deny, p.Source)
		}
	}
	for _, note := range []string{
		"field byId of edge.v1.Outer is left out: a map keyed by int32, where only a string key is read",
		"field none of edge.v1.Outer.choice is none2",
		"field func of edge.v1.Outer.Inner is func_",
		"edge.v1.MOOD_GLAD is left out: it aliases MOOD_HAPPY",
		"edge.v1.Server is declared as Server2: Server is taken or reserved",
		"edge.v1.Edge.Find has no component: its request holds list, a list or a map, and a query's key is compared",
		"edge.v1.Edge.Chat is left out: a streaming method is not called yet",
	} {
		if !slices.Contains(p.Notes, note) {
			t.Errorf("no note %q in %q", note, p.Notes)
		}
	}
}

func TestRefused(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"syntax = \"proto3\";\nimport \"other.proto\";\n", "only protobuf's own are"},
		{"syntax = \"proto3\";\nmessage {\n", "edge.proto:2:9"},
	} {
		_, err := Generate(c.src, "edge.proto")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("Generate(%q) = %v, want an error containing %q", c.src, err, c.want)
		}
	}
}
