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
	n := 0
	code, message = Stream(ctx, server, method, [][]byte{request}, func(m []byte) bool {
		response = m
		n++
		return true
	})
	if code == 0 && n != 1 {
		return nil, internal, "a unary call answered " + strconv.Itoa(n) + " messages"
	}
	return response, code, message
}

// Stream makes a call of method sending each of requests, in order, and hands
// each response message to each as it arrives, until the server ends the
// call, each answers false, or ctx ends. code and message are as Invoke's;
// each answering false ends the call with code -1.
func Stream(ctx context.Context, server, method string, requests [][]byte, each func([]byte) bool) (code int, message string) {
	if ctx.Err() != nil {
		return ended(ctx)
	}
	if server == "" {
		return unavailable, "no server: provide the service's address"
	}
	secure := strings.HasPrefix(server, "https://")
	host := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(server, "https://"), "http://"), "/")
	var framed []byte
	for _, r := range requests {
		framed = append(framed, 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint32(framed[len(framed)-4:], uint32(len(r)))
		framed = append(framed, r...)
	}
	scheme := "http://"
	if secure {
		scheme = "https://"
	}
	// A call each stops ends its stream, whatever ctx's lifetime.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, scheme+host+method, bytes.NewReader(framed))
	if err != nil {
		return internal, err.Error()
	}
	req.Header.Set("content-type", "application/grpc")
	req.Header.Set("te", "trailers")
	resp, err := transport(secure).RoundTrip(req)
	if err != nil {
		if ctx.Err() != nil {
			return ended(ctx)
		}
		return unavailable, err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fromHTTP(resp.StatusCode), "HTTP " + strconv.Itoa(resp.StatusCode)
	}
	var head [5]byte
	for {
		if _, err := io.ReadFull(resp.Body, head[:]); err != nil {
			if err == io.EOF {
				break
			}
			if ctx.Err() != nil {
				return ended(ctx)
			}
			if err == io.ErrUnexpectedEOF {
				return internal, "a response message cut short"
			}
			return unavailable, err.Error()
		}
		if head[0] != 0 {
			return internal, "a response message the client cannot read"
		}
		m := make([]byte, binary.BigEndian.Uint32(head[1:]))
		if _, err := io.ReadFull(resp.Body, m); err != nil {
			if ctx.Err() != nil {
				return ended(ctx)
			}
			return internal, "a response message cut short"
		}
		if !each(m) {
			return -1, ""
		}
		if ctx.Err() != nil {
			return ended(ctx)
		}
	}
	// A call that fails before answering carries its status in the headers,
	// with no trailers after them.
	status, text := resp.Header.Get("grpc-status"), resp.Header.Get("grpc-message")
	if status == "" {
		status, text = resp.Trailer.Get("grpc-status"), resp.Trailer.Get("grpc-message")
	}
	code, err = strconv.Atoi(status)
	if err != nil {
		return internal, "the response has no grpc-status"
	}
	if code != 0 {
		// grpc-message is percent-encoded UTF-8, where `+` is itself.
		if s, err := url.PathUnescape(text); err == nil {
			text = s
		}
		return code, text
	}
	return 0, ""
}

const (
	cancelled   = 1
	deadline    = 4
	internal    = 13
	unavailable = 14
)

func ended(ctx context.Context) (int, string) {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return deadline, "deadline exceeded"
	}
	return cancelled, "cancelled"
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
