// Package testgrpc is the gRPC server the programs `sngl test` runs call: the
// script harness's (cmd/sngl/script_test.go), which they find at Addr. It
// reads and writes its messages with protobuf's own runtime, so a program's
// encoding is checked against the reference rather than against itself.
package testgrpc

import (
	"context"
	"fmt"
	"io"
	"net"
	"time"

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
  // Answers books 1 to id, one at a time.
  rpc Watch(GetBookRequest) returns (stream Book);
  // Answers one book: its id the sum of the ids sent, its title how many.
  rpc Collect(stream GetBookRequest) returns (Book);
  // Answers a book per request, of its id.
  rpc Echo(stream GetBookRequest) returns (stream Book);
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
		var reqs []*dynamicpb.Message
		for {
			var in []byte
			if err := stream.RecvMsg(&in); err == io.EOF {
				break
			} else if err != nil {
				return err
			}
			req := dynamicpb.NewMessage(m.Input())
			if err := proto.Unmarshal(in, req); err != nil {
				return status.Errorf(codes.InvalidArgument, "request: %v", err)
			}
			reqs = append(reqs, req)
		}
		if !m.IsStreamingClient() && len(reqs) != 1 {
			return status.Errorf(codes.InvalidArgument, "%d requests to a unary method", len(reqs))
		}
		if m.IsStreamingServer() {
			return streamOut(stream.Context(), m, reqs, func(resp proto.Message) error {
				out, err := proto.Marshal(resp)
				if err != nil {
					return err
				}
				return stream.SendMsg(out)
			})
		}
		resp, err := answer(m, reqs)
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
//	Collect   a book whose id is the sum of the ids sent, titled "n books"
func answer(m protoreflect.MethodDescriptor, reqs []*dynamicpb.Message) (proto.Message, error) {
	book := dynamicpb.NewMessage(m.Output())
	fields := book.Descriptor().Fields()
	req := reqs[0]
	switch m.Name() {
	case "Collect":
		var sum int64
		for _, r := range reqs {
			sum += r.Get(r.Descriptor().Fields().ByName("id")).Int()
		}
		book.Set(fields.ByName("id"), protoreflect.ValueOfInt64(sum))
		book.Set(fields.ByName("title"), protoreflect.ValueOfString(fmt.Sprintf("%d books", len(reqs))))
		return book, nil
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

// streamOut sends what a streaming method of m answers reqs:
//
//	Watch  books 1 to id, then OK; for id 404 book 1 and then NOT_FOUND; for
//	       id 0 a book every 20ms until the call is cancelled
//	Echo   a book per request, of its id, titled "echo <id>"
func streamOut(ctx context.Context, m protoreflect.MethodDescriptor, reqs []*dynamicpb.Message, send func(proto.Message) error) error {
	book := func(id int64, title string) proto.Message {
		b := dynamicpb.NewMessage(m.Output())
		b.Set(b.Descriptor().Fields().ByName("id"), protoreflect.ValueOfInt64(id))
		b.Set(b.Descriptor().Fields().ByName("title"), protoreflect.ValueOfString(title))
		return b
	}
	idOf := func(r *dynamicpb.Message) int64 { return r.Get(r.Descriptor().Fields().ByName("id")).Int() }
	switch m.Name() {
	case "Watch":
		id := idOf(reqs[0])
		switch id {
		case 404:
			if err := send(book(1, "Book 1")); err != nil {
				return err
			}
			return status.Error(codes.NotFound, "the shelf is gone")
		case 0:
			for i := int64(1); ; i++ {
				if err := send(book(i, fmt.Sprintf("Book %d", i))); err != nil {
					return err
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(20 * time.Millisecond):
				}
			}
		}
		for i := int64(1); i <= id; i++ {
			if err := send(book(i, fmt.Sprintf("Book %d", i))); err != nil {
				return err
			}
		}
		return nil
	case "Echo":
		for _, r := range reqs {
			if err := send(book(idOf(r), fmt.Sprintf("echo %d", idOf(r)))); err != nil {
				return err
			}
		}
		return nil
	}
	return status.Errorf(codes.Unimplemented, "no method %s", m.Name())
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
