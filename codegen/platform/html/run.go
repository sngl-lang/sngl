//go:build !js

package html

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

func init() {
	codegen.RegisterCommand("html.serve", func(_ *ir.StructLit, args []any) (any, error) {
		dir, _ := args[0].(string)
		listen, _ := args[1].(string)
		return nil, serve(dir, listen)
	})
}

// serve answers `html.serve`, html's run command: it serves the generated
// static assets over HTTP at addr, ":0" when empty.
func serve(dir, addr string) error {
	if addr == "" {
		addr = ":0"
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}

	url := linkURL(ln.Addr())
	slog.Info("serve", "dir", dir, "addr", ln.Addr().String())
	fmt.Println(url)

	return http.Serve(ln, http.FileServer(http.Dir(dir)))
}

// linkURL builds a user-facing URL from the listener address, substituting
// loopback for the unspecified addresses (0.0.0.0, ::) that :0 binds to.
func linkURL(a net.Addr) string {
	tcp, ok := a.(*net.TCPAddr)
	if !ok {
		return "http://" + a.String()
	}
	host := tcp.IP.String()
	if tcp.IP == nil || tcp.IP.IsUnspecified() {
		host = "localhost"
	} else if tcp.IP.To4() == nil {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("http://%s:%d", host, tcp.Port)
}
