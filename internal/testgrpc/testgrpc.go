// Package testgrpc is the gRPC server the programs `sngl test` runs call: the
// script harness's (cmd/sngl/script_test.go), which they find at Addr. It
// reads and writes its messages with protobuf's own runtime, so a program's
// encoding is checked against the reference rather than against itself.
package testgrpc

import (
	"context"
	"fmt"
	"net"

	"github.com/bufbuild/protocompile"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/mem"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	"duckfam.us/sngl/pkg/go/snglgrpc"
)

// Addr is the address a program calls the server at, fixed for the reason
// testhttp.Addr is.
const Addr = "127.0.0.1:47820"

// Proto is the service the server offers. A script declares its own copy
// beside the program importing it.
const Proto = `syntax = "proto3";

package library.v1;

import "google/protobuf/timestamp.proto";

// A book on a shelf.
message Book {
  // The book's id.
  int64 id = 1;
  string title = 2;
  repeated string tags = 3;
  BookKind kind = 4;
  optional int32 pages = 5;
  google.protobuf.Timestamp added = 6;
  map<string, double> scores = 7;
  bytes cover = 8;
  repeated sint32 ranks = 9;
  float rating = 10;
  fixed64 isbn = 11;
  bool lent = 12;
  // Where the book is.
  oneof place {
    string shelf = 13;
    Loan loan = 14;
    int32 bin = 15;
  }
}

message Loan {
  string to = 1;
  int64 due = 2;
}

enum BookKind {
  BOOK_KIND_UNSPECIFIED = 0;
  BOOK_KIND_HARD_COVER = 1;
  BOOK_KIND_PAPERBACK = 2;
}

message GetBookRequest {
  int64 id = 1;
}

message AddBookRequest {
  Book book = 1;
}

service Library {
  // Answers one book.
  rpc GetBook(GetBookRequest) returns (Book) {
    option idempotency_level = NO_SIDE_EFFECTS;
  }
  // Answers the book it was handed, its id 99 and its title marked added.
  rpc AddBook(AddBookRequest) returns (Book);
}
`

// Start serves the service for the life of the process at a port the system
// picks, and has snglgrpc dial that port for Addr, as testhttp.Start does
// for http.
func Start() error {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	actual := l.Addr().String()
	var d net.Dialer
	snglgrpc.SetDialer(func(ctx context.Context, addr string) (net.Conn, error) {
		if addr == Addr {
			addr = actual
		}
		return d.DialContext(ctx, "tcp", addr)
	})
	return serveOn(l)
}

// StartAt serves the service at Addr, where a program in any process finds
// it.
func StartAt() error {
	l, err := net.Listen("tcp", Addr)
	if err != nil {
		return err
	}
	return serveOn(l)
}

func serveOn(l net.Listener) error {
	files, err := (&protocompile.Compiler{
		Resolver: protocompile.WithStandardImports(&protocompile.SourceResolver{
			Accessor: protocompile.SourceAccessorFromMap(map[string]string{"library.proto": Proto}),
		}),
	}).Compile(context.Background(), "library.proto")
	if err != nil {
		return err
	}
	svc := files[0].Services().ByName("Library")
	s := grpc.NewServer(grpc.ForceServerCodecV2(rawCodec{}), grpc.UnknownServiceHandler(func(_ any, stream grpc.ServerStream) error {
		name, _ := grpc.MethodFromServerStream(stream)
		var m protoreflect.MethodDescriptor
		for i := range svc.Methods().Len() {
			if x := svc.Methods().Get(i); "/"+string(svc.FullName())+"/"+string(x.Name()) == name {
				m = x
			}
		}
		if m == nil {
			return status.Errorf(codes.Unimplemented, "no method %s", name)
		}
		var in []byte
		if err := stream.RecvMsg(&in); err != nil {
			return err
		}
		req := dynamicpb.NewMessage(m.Input())
		if err := proto.Unmarshal(in, req); err != nil {
			return status.Errorf(codes.InvalidArgument, "request: %v", err)
		}
		resp, err := answer(m, req)
		if err != nil {
			return err
		}
		out, err := proto.Marshal(resp)
		if err != nil {
			return err
		}
		return stream.SendMsg(out)
	}))
	go func() { _ = s.Serve(l) }()
	return nil
}

// answer is what the server answers a request of m:
//
//	GetBook   id 404 NOT_FOUND, id 16 UNAUTHENTICATED, id 2 a book on the
//	          shelf "", any other a book of that id with every field set,
//	          its isbn past 32 bits for id 64 alone
//	AddBook   the book handed, its id 99 and "added: " before its title
func answer(m protoreflect.MethodDescriptor, req *dynamicpb.Message) (proto.Message, error) {
	book := dynamicpb.NewMessage(m.Output())
	fields := book.Descriptor().Fields()
	switch m.Name() {
	case "GetBook":
		id := req.Get(req.Descriptor().Fields().ByName("id")).Int()
		switch id {
		case 404:
			return nil, status.Errorf(codes.NotFound, "no book %d", id)
		case 16:
			return nil, status.Error(codes.Unauthenticated, "who are you")
		}
		book.Set(fields.ByName("id"), protoreflect.ValueOfInt64(id))
		book.Set(fields.ByName("title"), protoreflect.ValueOfString(fmt.Sprintf("Book %d", id)))
		tags := book.Mutable(fields.ByName("tags")).List()
		tags.Append(protoreflect.ValueOfString("old"))
		tags.Append(protoreflect.ValueOfString("rare"))
		book.Set(fields.ByName("kind"), protoreflect.ValueOfEnum(2))
		book.Set(fields.ByName("pages"), protoreflect.ValueOfInt32(320))
		added := book.Mutable(fields.ByName("added")).Message()
		added.Set(added.Descriptor().Fields().ByName("seconds"), protoreflect.ValueOfInt64(1700000000))
		added.Set(added.Descriptor().Fields().ByName("nanos"), protoreflect.ValueOfInt32(5))
		book.Mutable(fields.ByName("scores")).Map().Set(protoreflect.ValueOfString("plot").MapKey(), protoreflect.ValueOfFloat64(4.5))
		book.Set(fields.ByName("cover"), protoreflect.ValueOfBytes([]byte("hi")))
		ranks := book.Mutable(fields.ByName("ranks")).List()
		ranks.Append(protoreflect.ValueOfInt32(-1))
		ranks.Append(protoreflect.ValueOfInt32(2))
		book.Set(fields.ByName("rating"), protoreflect.ValueOfFloat32(0.5))
		isbn := uint64(978)
		if id == 64 {
			isbn = 9780000000002
		}
		book.Set(fields.ByName("isbn"), protoreflect.ValueOfUint64(isbn))
		book.Set(fields.ByName("lent"), protoreflect.ValueOfBool(true))
		if id == 2 {
			book.Set(fields.ByName("shelf"), protoreflect.ValueOfString(""))
			return book, nil
		}
		loan := book.Mutable(fields.ByName("loan")).Message()
		loan.Set(loan.Descriptor().Fields().ByName("to"), protoreflect.ValueOfString("Ada"))
		loan.Set(loan.Descriptor().Fields().ByName("due"), protoreflect.ValueOfInt64(5))
		return book, nil
	case "AddBook":
		f := req.Descriptor().Fields().ByName("book")
		if !req.Has(f) {
			return nil, status.Error(codes.InvalidArgument, "no book")
		}
		got := req.Get(f).Message()
		got.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
			book.Set(fields.ByNumber(fd.Number()), v)
			return true
		})
		book.Set(fields.ByName("id"), protoreflect.ValueOfInt64(99))
		book.Set(fields.ByName("title"), protoreflect.ValueOfString("added: "+book.Get(fields.ByName("title")).String()))
		return book, nil
	}
	return nil, status.Errorf(codes.Unimplemented, "no method %s", m.Name())
}

type rawCodec struct{}

func (rawCodec) Name() string { return "proto" }

func (rawCodec) Marshal(v any) (mem.BufferSlice, error) {
	return mem.BufferSlice{mem.SliceBuffer(v.([]byte))}, nil
}

func (rawCodec) Unmarshal(data mem.BufferSlice, v any) error {
	*v.(*[]byte) = data.Materialize()
	return nil
}
