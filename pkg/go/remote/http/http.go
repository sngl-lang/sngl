// Package http is the Go transport behind sngl:remote/http. The SNGL package
// declares `get` and this implements it; the language override in
// sngl:language/go is the one line that connects them.
//
// It is a package rather than an intrinsic emitter because there is nothing
// here a code generator needs to build a string for. An emitter would spell
// this out at every call site; a function is written once and testable on its
// own.
package http

import (
	"io"
	"net/http"
	"strconv"
	"strings"

	remote "git.duckfam.us/jonathan/sngl/pkg/go/remote"
)

// Response is what one request answered with. It mirrors sngl:remote/http's
// Result rather than being it: the SNGL declaration is the program's own type,
// and the override converts at the boundary, field by field. That is what a
// boundary is for, and it keeps the SNGL package free of any one language's
// spelling.
type Response struct {
	Status  int
	Body    string
	Headers map[string]string
}

// Get performs one request.
//
// A failure is returned as a remote.Failure so the box records what the program
// should do next rather than a string it would have to parse: a status the peer
// chose is classified, and a request that never arrived is transport.
func Get(url string) (Response, error) {
	resp, err := http.Get(url)
	if err != nil {
		return Response{}, remote.Failure{
			Kind:    kindForTransportError(err),
			Message: err.Error(),
		}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		// The peer answered and then the read failed, which is neither the
		// peer's fault nor a request to retry differently.
		return Response{}, remote.Failure{
			Kind:    remote.Transport,
			Message: err.Error(),
			Code:    strconv.Itoa(resp.StatusCode),
		}
	}

	out := Response{
		Status:  resp.StatusCode,
		Body:    string(body),
		Headers: flatten(resp.Header),
	}
	if k, failed := kindForStatus(resp.StatusCode); failed {
		return out, remote.Failure{
			Kind:    k,
			Message: resp.Status,
			Code:    strconv.Itoa(resp.StatusCode),
		}
	}
	return out, nil
}

// kindForStatus classifies a status by what the program should do next, which
// is the only question a UI asks of a failure. 4xx and 5xx are separate for
// that reason: retrying a 400 fails identically, retrying a 503 may work.
func kindForStatus(code int) (remote.FailureKind, bool) {
	switch {
	case code >= 200 && code < 400:
		return 0, false
	case code == 401 || code == 403:
		return remote.Auth, true
	case code == 408 || code == 504:
		return remote.Timeout, true
	case code >= 400 && code < 500:
		return remote.Request, true
	default:
		return remote.Server, true
	}
}

// kindForTransportError separates a deadline from everything else. Go reports a
// timeout through an interface rather than a sentinel, so this asks the error.
func kindForTransportError(err error) remote.FailureKind {
	type timeout interface{ Timeout() bool }
	if t, ok := err.(timeout); ok && t.Timeout() {
		return remote.Timeout
	}
	return remote.Transport
}

// flatten keeps one value per header. A repeated header joins with ", ", which
// is what the field is defined to mean when it repeats.
func flatten(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[k] = strings.Join(v, ", ")
	}
	return out
}
