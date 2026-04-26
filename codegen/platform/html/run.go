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

// Run implements codegen.Runner. It serves the generated static assets over
// HTTP. The listen address comes from the "listen" option (default ":0").
func (g *Generator) Run(dir string, opts *ir.StructLit, _ []string) error {
	var cfg htmlConfig
	if err := codegen.ApplyOptions(&cfg, opts); err != nil {
		return fmt.Errorf("html: %w", err)
	}
	addr := cfg.Listen
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
