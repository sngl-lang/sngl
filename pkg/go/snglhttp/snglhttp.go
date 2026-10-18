// Package snglhttp is the runtime behind sngl:http's send for a program
// compiled to Go, and for the interpreter.
//
// It answers a failure only where no response arrived: a status is the
// server's answer, read off the response, and classifying it is SNGL's own
// `Response.checked`, so every target classifies alike.
package snglhttp

import (
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
)

// Response is what a server answered.
type Response struct {
	Status  int
	Headers map[string]string
	Body    string
}

// Failure is why no response arrived. Timeout says the request reached for
// the server and got no answer in time; anything else never reached it.
type Failure struct {
	Timeout bool
	Message string
}

// Send makes one request and waits for its answer. body is nil for none.
func Send(method, url string, headers map[string]string, body *string) (Response, *Failure) {
	var r io.Reader
	if body != nil {
		r = strings.NewReader(*body)
	}
	req, err := http.NewRequest(strings.ToUpper(method), url, r)
	if err != nil {
		return Response{}, &Failure{Message: err.Error()}
	}
	for _, k := range sortedKeys(headers) {
		req.Header.Set(k, headers[k])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Response{}, failure(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return Response{}, failure(err)
	}
	out := Response{Status: resp.StatusCode, Headers: map[string]string{}, Body: string(data)}
	for k, vs := range resp.Header {
		out.Headers[strings.ToLower(k)] = strings.Join(vs, ", ")
	}
	return out, nil
}

func failure(err error) *Failure {
	var ne net.Error
	timeout := errors.Is(err, os.ErrDeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout())
	return &Failure{Timeout: timeout, Message: err.Error()}
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
