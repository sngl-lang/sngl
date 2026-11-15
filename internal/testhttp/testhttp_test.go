package testhttp

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// A request for Addr reaches Start's server, whatever holds Addr: here a
// listener that never answers, standing in for another run's server.
func TestStartAnswersAddrWhileItIsTaken(t *testing.T) {
	if l, err := net.Listen("tcp", Addr); err == nil {
		t.Cleanup(func() {
			if err := l.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	if err := Start(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+Addr+"/status/201", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated || string(body) != "status 201" {
		t.Fatalf("got %d %q, want 201 %q", resp.StatusCode, body, "status 201")
	}
}
