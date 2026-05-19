package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
)

// Server is the SNGL language server.
type Server struct {
	ws     *workspace
	reader *bufio.Reader
	writer io.Writer
	mu     sync.Mutex // protects writes
	log    *log.Logger
}

// New creates a new LSP server.
func New() *Server {
	return &Server{
		ws:  newWorkspace(),
		log: log.New(os.Stderr, "[sngl-lsp] ", log.LstdFlags),
	}
}

// RunStdio runs the server over stdin/stdout.
func (s *Server) RunStdio() error {
	s.reader = bufio.NewReader(os.Stdin)
	s.writer = os.Stdout
	return s.serve()
}

// RunTCP runs the server on a TCP port.
func (s *Server) RunTCP(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	s.log.Printf("listening on %s", addr)
	for {
		conn, err := ln.Accept()
		if err != nil {
			s.log.Printf("accept error: %v", err)
			continue
		}
		go func() {
			srv := &Server{
				ws:     newWorkspace(),
				reader: bufio.NewReader(conn),
				writer: conn,
				log:    s.log,
			}
			if err := srv.serve(); err != nil {
				s.log.Printf("session error: %v", err)
			}
			conn.Close()
		}()
	}
}

func (s *Server) serve() error {
	for {
		msg, err := s.readMessage()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("read: %w", err)
		}

		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id,omitempty"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params,omitempty"`
		}
		if err := json.Unmarshal(msg, &req); err != nil {
			s.log.Printf("unmarshal error: %v", err)
			continue
		}

		isNotification := len(req.ID) == 0 || string(req.ID) == "null"

		switch req.Method {
		case "initialize":
			s.handleInitialize(req.ID, req.Params)
		case "initialized":
			// no-op
		case "shutdown":
			s.sendResult(req.ID, nil)
		case "exit":
			return nil
		case "textDocument/didOpen":
			s.handleDidOpen(req.Params)
		case "textDocument/didChange":
			s.handleDidChange(req.Params)
		case "textDocument/didClose":
			s.handleDidClose(req.Params)
		case "textDocument/didSave":
			s.handleDidSave(req.Params)
		case "textDocument/hover":
			s.handleHover(req.ID, req.Params)
		case "textDocument/completion":
			s.handleCompletion(req.ID, req.Params)
		case "textDocument/semanticTokens/full":
			s.handleSemanticTokensFull(req.ID, req.Params)
		case "textDocument/documentColor":
			s.handleDocumentColor(req.ID, req.Params)
		case "textDocument/colorPresentation":
			s.handleColorPresentation(req.ID, req.Params)
		case "textDocument/inlayHint":
			s.handleInlayHint(req.ID, req.Params)
		default:
			if !isNotification {
				s.sendError(req.ID, -32601, "method not found: "+req.Method)
			}
		}
	}
}

func (s *Server) readMessage() ([]byte, error) {
	contentLength := -1
	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if after, ok := strings.CutPrefix(line, "Content-Length:"); ok {
			val := strings.TrimSpace(after)
			contentLength, _ = strconv.Atoi(val)
		}
	}
	if contentLength <= 0 {
		return nil, fmt.Errorf("missing Content-Length")
	}
	body := make([]byte, contentLength)
	_, err := io.ReadFull(s.reader, body)
	return body, err
}

func (s *Server) sendMessage(msg any) {
	data, err := json.Marshal(msg)
	if err != nil {
		s.log.Printf("marshal error: %v", err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
	s.writer.Write([]byte(header))
	s.writer.Write(data)
}

func (s *Server) sendResult(id json.RawMessage, result any) {
	s.sendMessage(map[string]any{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(id),
		"result":  result,
	})
}

func (s *Server) sendError(id json.RawMessage, code int, message string) {
	s.sendMessage(map[string]any{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(id),
		"error":   map[string]any{"code": code, "message": message},
	})
}

func (s *Server) notify(method string, params any) {
	s.sendMessage(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
}

func (s *Server) publishDiagnostics(uri string, diags []Diagnostic) {
	if diags == nil {
		diags = []Diagnostic{}
	}
	s.notify("textDocument/publishDiagnostics", PublishDiagnosticsParams{
		URI:         uri,
		Diagnostics: diags,
	})
}
