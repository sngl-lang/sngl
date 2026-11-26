package snglgrpc

import (
	"context"
	"net"
)

// Invoke answers UNAVAILABLE in a browser, which opens no HTTP/2 connection
// a gRPC call needs.
func Invoke(ctx context.Context, server, method string, request []byte) ([]byte, int, string) {
	return nil, 14, "a gRPC call cannot be made from a browser"
}

func Stream(ctx context.Context, server, method string, requests [][]byte, each func([]byte) bool) (int, string) {
	return 14, "a gRPC call cannot be made from a browser"
}

func SetDialer(func(ctx context.Context, addr string) (net.Conn, error)) {}
