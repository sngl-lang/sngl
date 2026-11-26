//go:build !js

// Package snglgrpc is the runtime behind sngl:grpc's call for a program
// compiled to Go, and for the interpreter.
//
// It carries bytes and nothing else: the program writes the request message
// and reads the response from its own types, and a call here frames them as
// gRPC over x/net's HTTP/2. grpc-go is not linked: its x/net/trace runs
// html/template, whose reflective method call keeps every exported method
// in the binary that links it (cmd/sngl/reflectmethod_test.go).
package snglgrpc

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/net/http2"
)

// Invoke makes one unary call of method at server with the request message's
// bytes, and answers the response message's. Where the call fails, code is
// the gRPC status number and message its text; a lifetime ended first is
// CANCELLED.
//
// server is `host:port`, called in plaintext, or `https://host[:port]`,
// called over TLS against the system's roots.
func Invoke(ctx context.Context, server, method string, request []byte) (response []byte, code int, message string) {
	if ctx.Err() != nil {
		return ended(ctx)
	}
	if server == "" {
		return nil, unavailable, "no server: provide the service's address"
	}
	secure := strings.HasPrefix(server, "https://")
	host := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(server, "https://"), "http://"), "/")
	framed := make([]byte, 5+len(request))
	binary.BigEndian.PutUint32(framed[1:], uint32(len(request)))
	copy(framed[5:], request)
	scheme := "http://"
	if secure {
		scheme = "https://"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, scheme+host+method, bytes.NewReader(framed))
	if err != nil {
		return nil, internal, err.Error()
	}
	req.Header.Set("content-type", "application/grpc")
	req.Header.Set("te", "trailers")
	resp, err := transport(secure).RoundTrip(req)
	if err != nil {
		if ctx.Err() != nil {
			return ended(ctx)
		}
		return nil, unavailable, err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fromHTTP(resp.StatusCode), "HTTP " + strconv.Itoa(resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		if ctx.Err() != nil {
			return ended(ctx)
		}
		return nil, unavailable, err.Error()
	}
	// A call that fails before answering carries its status in the headers,
	// with no trailers after them.
	status, text := resp.Header.Get("grpc-status"), resp.Header.Get("grpc-message")
	if status == "" {
		status, text = resp.Trailer.Get("grpc-status"), resp.Trailer.Get("grpc-message")
	}
	code, err = strconv.Atoi(status)
	if err != nil {
		return nil, internal, "the response has no grpc-status"
	}
	if code != 0 {
		// grpc-message is percent-encoded UTF-8, where `+` is itself.
		if s, err := url.PathUnescape(text); err == nil {
			text = s
		}
		return nil, code, text
	}
	if len(body) < 5 || body[0] != 0 {
		return nil, internal, "a response message the client cannot read"
	}
	n := binary.BigEndian.Uint32(body[1:5])
	if uint64(n) > uint64(len(body)-5) {
		return nil, internal, "a response message cut short"
	}
	return body[5 : 5+n], 0, ""
}

const (
	cancelled   = 1
	deadline    = 4
	internal    = 13
	unavailable = 14
)

func ended(ctx context.Context) ([]byte, int, string) {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, deadline, "deadline exceeded"
	}
	return nil, cancelled, "cancelled"
}

// fromHTTP is gRPC's mapping of an HTTP status that is not one of its own
// answers.
func fromHTTP(status int) int {
	switch status {
	case 400:
		return internal
	case 401:
		return 16
	case 403:
		return 7
	case 404:
		return 12
	case 429, 502, 503, 504:
		return unavailable
	}
	return 2
}

var (
	mu     sync.Mutex
	plain  *http2.Transport
	tlsT   *http2.Transport
	dialer func(ctx context.Context, addr string) (net.Conn, error)
)

// SetDialer has every connection made after it dial through d: a test
// server serving at a port of its own answers the address a program writes.
func SetDialer(d func(ctx context.Context, addr string) (net.Conn, error)) {
	mu.Lock()
	defer mu.Unlock()
	dialer, plain, tlsT = d, nil, nil
}

// transport is made once per kind, since an http2.Transport keeps one
// connection per host and reuses it.
func transport(secure bool) *http2.Transport {
	mu.Lock()
	defer mu.Unlock()
	dial := dialer
	if dial == nil {
		var d net.Dialer
		dial = func(ctx context.Context, addr string) (net.Conn, error) { return d.DialContext(ctx, "tcp", addr) }
	}
	if secure {
		if tlsT == nil {
			tlsT = &http2.Transport{DialTLSContext: func(ctx context.Context, _, addr string, cfg *tls.Config) (net.Conn, error) {
				c, err := dial(ctx, addr)
				if err != nil {
					return nil, err
				}
				tc := tls.Client(c, cfg)
				if err := tc.HandshakeContext(ctx); err != nil {
					c.Close()
					return nil, err
				}
				return tc, nil
			}}
		}
		return tlsT
	}
	if plain == nil {
		// A server written `host:port` is called in plaintext, which HTTP/2
		// speaks only to a client that assumes it.
		plain = &http2.Transport{AllowHTTP: true, DialTLSContext: func(ctx context.Context, _, addr string, _ *tls.Config) (net.Conn, error) {
			return dial(ctx, addr)
		}}
	}
	return plain
}
