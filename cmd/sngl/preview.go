package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/internal/snapshot"
	"git.duckfam.us/jonathan/sngl/ir"
	"git.duckfam.us/jonathan/sngl/lib"
	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
)

var previewCmd = &cobra.Command{
	Use:   "preview [file.sngl]",
	Short: "Live-preview an SNGL app",
	Args:  cobra.ExactArgs(1),
	RunE:  runPreview,
}

func init() {
	previewCmd.Flags().Int("port", 3000, "server port")
	previewCmd.Flags().Bool("open", true, "auto-open browser")
}

type previewServer struct {
	mu         sync.RWMutex
	sourceFile string
	sourceDir  string
	activeLang string
	activePlat string
	content    []byte
	doc        *ast.Document
	clientsMu  sync.Mutex
	clients    map[chan struct{}]struct{}

	// Stdlib schemas, so the editor can show the unset properties too.
	schemas checker.SchemaRegistry

	// Synced from the browser.
	runtimeState map[string]any
}

func runPreview(cmd *cobra.Command, args []string) error {
	port, _ := cmd.Flags().GetInt("port")
	autoOpen, _ := cmd.Flags().GetBool("open")

	sourceFile, err := filepath.Abs(args[0])
	if err != nil {
		return err
	}

	schemas := libraryComponentSchemas()

	s := &previewServer{
		sourceFile: sourceFile,
		sourceDir:  filepath.Dir(sourceFile),
		clients:    make(map[chan struct{}]struct{}),
		schemas:    schemas,
	}

	if err := s.recompile(); err != nil {
		return fmt.Errorf("initial compile: %w", err)
	}

	go s.watch()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleWrapper)
	mux.HandleFunc("GET /preview", s.handlePreview)
	mux.HandleFunc("GET /events", s.handleEvents)
	mux.HandleFunc("GET /targets", s.handleTargets)
	mux.HandleFunc("GET /switch", s.handleSwitch)
	mux.HandleFunc("GET /app", s.handleAppGet)
	mux.HandleFunc("POST /app/state", s.handleAppStatePost)
	mux.HandleFunc("GET /node", s.handleNodeGet)
	mux.HandleFunc("POST /node", s.handleNodePost)

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	url := fmt.Sprintf("http://%s", addr)
	fmt.Fprintf(os.Stderr, "Preview server listening on %s\n", url)

	if autoOpen {
		go openBrowser(url)
	}

	return http.ListenAndServe(addr, mux)
}

func (s *previewServer) recompile() error {
	f, err := os.Open(s.sourceFile)
	if err != nil {
		return err
	}
	doc, err := parseSNGL(s.sourceFile, f)
	f.Close()
	if err != nil {
		return err
	}

	pkg, err := checkDoc(doc, s.sourceDir, true)
	if err != nil {
		return err
	}

	s.mu.Lock()

	if s.activeLang == "" || s.activePlat == "" {
		if pkg != nil && len(pkg.Outputs) > 0 {
			s.activeLang = pkg.Outputs[0].Lang
			s.activePlat = pkg.Outputs[0].Platform
		} else {
			s.activeLang = "js"
			s.activePlat = "html"
		}
	}

	activeLang := s.activeLang
	activePlat := s.activePlat
	s.doc = doc
	s.mu.Unlock()

	html, err := snapshot.CompilePreviewHTML(s.sourceFile, activePlat, activeLang)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.content = html
	s.mu.Unlock()

	return nil
}

func (s *previewServer) broadcast() {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	for ch := range s.clients {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (s *previewServer) watch() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("fsnotify: %v", err)
		return
	}
	defer watcher.Close()

	if err := watcher.Add(s.sourceFile); err != nil {
		log.Printf("fsnotify watch: %v", err)
		return
	}

	var debounce *time.Timer
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Op&(fsnotify.Write|fsnotify.Create) == 0 {
				continue
			}
			if debounce != nil {
				debounce.Stop()
			}
			debounce = time.AfterFunc(100*time.Millisecond, func() {
				if err := s.recompile(); err != nil {
					log.Printf("recompile: %v", err)
					return
				}
				s.broadcast()
			})
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Printf("fsnotify error: %v", err)
		}
	}
}

func (s *previewServer) handleWrapper(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(wrapperHTML))
}

func (s *previewServer) handlePreview(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	content := s.content
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.URL.Query().Get("raw") == "1" {
		w.Write(content)
		return
	}
	html := strings.Replace(string(content), "</body>", liveReloadScript+"\n</body>", 1)
	w.Write([]byte(html))
}

func (s *previewServer) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := make(chan struct{}, 1)
	s.clientsMu.Lock()
	s.clients[ch] = struct{}{}
	s.clientsMu.Unlock()

	defer func() {
		s.clientsMu.Lock()
		delete(s.clients, ch)
		s.clientsMu.Unlock()
	}()

	fmt.Fprintf(w, "event: connected\ndata: {}\n\n")
	flusher.Flush()

	for {
		select {
		case <-ch:
			fmt.Fprintf(w, "event: reload\ndata: {}\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *previewServer) handleTargets(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	doc := s.doc
	activeLang := s.activeLang
	activePlat := s.activePlat
	s.mu.RUnlock()

	type target struct {
		Platform string `json:"platform"`
		Lang     string `json:"lang"`
		Active   bool   `json:"active"`
	}

	seen := map[string]bool{}
	var targets []target

	if doc != nil {
		_ = doc // outputs are resolved during check; fallback below covers all platforms
	}

	for _, plat := range codegen.Platforms() {
		pg := codegen.LookupPlatform(plat)
		for _, lang := range pg.SupportedLangs() {
			key := plat + "/" + lang
			if seen[key] {
				continue
			}
			seen[key] = true
			targets = append(targets, target{
				Platform: plat,
				Lang:     lang,
				Active:   plat == activePlat && lang == activeLang,
			})
		}
	}

	resp := struct {
		File    string   `json:"file"`
		Targets []target `json:"targets"`
	}{
		File:    filepath.Base(s.sourceFile),
		Targets: targets,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *previewServer) handleSwitch(w http.ResponseWriter, r *http.Request) {
	plat := r.URL.Query().Get("platform")
	lang := r.URL.Query().Get("lang")
	if plat == "" || lang == "" {
		http.Error(w, "platform and lang required", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	s.activePlat = plat
	s.activeLang = lang
	s.mu.Unlock()

	if err := s.recompile(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.broadcast()
	w.WriteHeader(http.StatusOK)
}

type nodeResponse struct {
	Component string              `json:"component"`
	Pos       nodePos             `json:"pos"`
	Props     map[string]propJSON `json:"props"`
	Styles    map[string]propJSON `json:"styles"`
	Events    []eventJSON         `json:"events"`
}

type nodePos struct {
	Line int `json:"line"`
	Col  int `json:"col"`
}

type propJSON struct {
	Literal any      `json:"literal,omitempty"`
	Expr    string   `json:"expr,omitempty"`
	Set     bool     `json:"set"`
	Type    string   `json:"type,omitempty"`
	Enum    []string `json:"enum,omitempty"`
}

type eventJSON struct {
	Name string `json:"name"`
	Set  bool   `json:"set"`
}

func exprToPropJSON(e ast.Expr, typeName string, enum []string) propJSON {
	p := propJSON{Set: true, Type: typeName, Enum: enum}
	switch v := e.(type) {
	case *ast.LiteralExpr:
		if sv, ok := v.StringValue(); ok {
			p.Literal = sv
		} else {
			p.Literal = v.Raw
		}
	default:
		p.Expr = parser.FormatExpr(e)
	}
	return p
}

func (s *previewServer) handleAppGet(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	doc := s.doc
	s.mu.RUnlock()

	type dataJSON struct {
		Name   string `json:"name"`
		Init   string `json:"init,omitempty"`
		Value  any    `json:"value,omitempty"`
		Type   string `json:"type,omitempty"`
		IsFunc bool   `json:"isFunc,omitempty"`
	}
	type computedJSON struct {
		Name string `json:"name"`
		Expr string `json:"expr"`
	}
	type appResponse struct {
		Data     []dataJSON     `json:"data"`
		Computed []computedJSON `json:"computed"`
	}

	resp := appResponse{
		Data:     []dataJSON{},
		Computed: []computedJSON{},
	}

	if doc != nil {
		for _, stmt := range doc.Stmts {
			switch s := stmt.(type) {
			case *ast.VarDecl:
				for _, spec := range s.Specs {
					for _, name := range spec.Names {
						dj := dataJSON{Name: name}
						if spec.Default != nil {
							dj.Init = parser.FormatExpr(spec.Default)
						}
						resp.Data = append(resp.Data, dj)
					}
				}
			case *ast.FuncDef:
				if s.Body != nil && len(s.Params.Params) == 0 {
					cj := computedJSON{Name: s.Name, Expr: parser.FormatExpr(s.Body)}
					resp.Computed = append(resp.Computed, cj)
				}
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *previewServer) handleAppStatePost(w http.ResponseWriter, r *http.Request) {
	var body struct {
		State map[string]any `json:"state"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.runtimeState = body.State
	s.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (s *previewServer) handleNodeGet(w http.ResponseWriter, r *http.Request) {
	line := queryInt(r, "line")
	col := queryInt(r, "col")
	if line == 0 || col == 0 {
		http.Error(w, "line and col required", http.StatusBadRequest)
		return
	}

	s.mu.RLock()
	doc := s.doc
	s.mu.RUnlock()

	if doc == nil {
		http.Error(w, "no document", http.StatusNotFound)
		return
	}

	vn := findNodeInDoc(doc, line, col)
	if vn == nil {
		http.Error(w, "node not found", http.StatusNotFound)
		return
	}

	compName := vnComponentName(vn)

	resp := nodeResponse{
		Component: compName,
		Pos:       nodePos{Line: vn.Pos.Line, Col: vn.Pos.Column},
		Props:     make(map[string]propJSON),
		Styles:    make(map[string]propJSON),
	}

	schema := s.schemas[compName]

	// Schema first, so a set prop overwrites its unset entry below.
	if schema != nil {
		for name, ps := range schema.Props {
			resp.Props[name] = propJSON{
				Type: typeToString(ps.Type),
				Enum: ps.Enum,
			}
		}
	}
	for _, a := range vn.Args.Args {
		arg, ok := a.(ast.Arg)
		if !ok || arg.Name == "" {
			continue
		}
		typeName := ""
		var enum []string
		if schema != nil {
			if ps, ok := schema.Props[arg.Name]; ok {
				typeName = typeToString(ps.Type)
				enum = ps.Enum
			}
		}
		resp.Props[arg.Name] = exprToPropJSON(arg.Value, typeName, enum)
	}

	// Only the styles the node sets, untyped: Style's fields are an ordinary
	// struct declaration and nothing here resolves them.
	for _, a := range vn.Args.Args {
		arg, ok := a.(ast.Arg)
		if !ok || arg.Name != "style" {
			continue
		}
		if se, ok := arg.Value.(*ast.StructExpr); ok {
			for _, f := range se.Fields {
				resp.Styles[f.Name] = exprToPropJSON(f.Value, "", nil)
			}
		}
	}

	setEvents := map[string]bool{}
	for _, a := range vn.Args.Args {
		if eh, ok := a.(ast.EventHandler); ok {
			setEvents[eh.Name] = true
		}
	}

	if schema != nil {
		for name := range schema.Events {
			resp.Events = append(resp.Events, eventJSON{Name: name, Set: setEvents[name]})
			delete(setEvents, name)
		}
	}
	for name := range setEvents {
		resp.Events = append(resp.Events, eventJSON{Name: name, Set: true})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// libraryComponentSchemas is every library package's component schemas in one
// map. The editor resolves a component by the name written in the source, which
// is the name in that file's scope; a name two packages declare resolves here to
// whichever package sorts last, and the editor shows its props.
func libraryComponentSchemas() checker.SchemaRegistry {
	out := checker.SchemaRegistry{}
	for _, pkg := range lib.PublicPackages() {
		for name, schema := range checker.PackageSchema(pkg) {
			out[name] = schema
		}
	}
	return out
}

func typeToString(t ir.Type) string {
	return t.String()
}

func vnComponentName(vn *ast.VisualNode) string {
	if id, ok := vn.Target.(*ast.IdentExpr); ok {
		return id.Name
	}
	return parser.FormatExpr(vn.Target)
}

func findNodeInDoc(doc *ast.Document, line, col int) *ast.VisualNode {
	for _, stmt := range doc.Stmts {
		if comp, ok := stmt.(*ast.ComponentDecl); ok {
			if vn := findNodeInStmts(comp.Body.Stmts, line, col); vn != nil {
				return vn
			}
		}
	}
	return nil
}

func findNodeInStmts(stmts []ast.Stmt, line, col int) *ast.VisualNode {
	for _, s := range stmts {
		vn, ok := s.(*ast.VisualNode)
		if !ok {
			continue
		}
		if vn.Pos.Line == line && vn.Pos.Column == col {
			return vn
		}
		if found := findNodeInStmts(vn.Block.Stmts, line, col); found != nil {
			return found
		}
	}
	return nil
}

func (s *previewServer) handleNodePost(w http.ResponseWriter, r *http.Request) {
	line := queryInt(r, "line")
	col := queryInt(r, "col")
	if line == 0 || col == 0 {
		http.Error(w, "line and col required", http.StatusBadRequest)
		return
	}

	var body struct {
		Props  map[string]string `json:"props"`
		Styles map[string]string `json:"styles"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	data, err := os.ReadFile(s.sourceFile)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	doc, err := parser.Parse(s.sourceFile, data)
	if err != nil {
		http.Error(w, "parse: "+err.Error(), http.StatusInternalServerError)
		return
	}

	vn := findNodeInDoc(doc, line, col)
	if vn == nil {
		http.Error(w, "node not found at position", http.StatusNotFound)
		return
	}

	for k, v := range body.Props {
		setArg(vn, k, parseSNGLValue(v))
	}
	if len(body.Styles) > 0 {
		var fields []ast.StructFieldLit
		for k, v := range body.Styles {
			fields = append(fields, ast.StructFieldLit{Name: k, Value: parseSNGLValue(v)})
		}
		setArg(vn, "style", &ast.StructExpr{Fields: fields})
	}

	formatted := parser.Format(doc)
	if err := os.WriteFile(s.sourceFile, []byte(formatted), 0o644); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// fsnotify will pick up the write → recompile → SSE reload
	w.WriteHeader(http.StatusOK)
}

func parseSNGLValue(s string) ast.Expr {
	var i int
	if _, err := fmt.Sscanf(s, "%d", &i); err == nil && fmt.Sprintf("%d", i) == s {
		return &ast.LiteralExpr{Kind: ast.LiteralInt, Raw: s}
	}
	var f float64
	if _, err := fmt.Sscanf(s, "%g", &f); err == nil {
		return &ast.LiteralExpr{Kind: ast.LiteralFloat, Raw: s}
	}
	if s == "true" || s == "false" {
		return &ast.LiteralExpr{Kind: ast.LiteralBool, Raw: s}
	}
	if strings.HasPrefix(s, "#") {
		return &ast.LiteralExpr{Kind: ast.LiteralColor, Raw: s}
	}
	return ast.NewStringLiteral(s)
}

func setArg(vn *ast.VisualNode, name string, val ast.Expr) {
	for i, a := range vn.Args.Args {
		if arg, ok := a.(ast.Arg); ok && arg.Name == name {
			vn.Args.Args[i] = ast.Arg{Name: name, Value: val}
			return
		}
	}
	vn.Args.Args = append(vn.Args.Args, ast.Arg{Name: name, Value: val})
}

func queryInt(r *http.Request, key string) int {
	var v int
	fmt.Sscanf(r.URL.Query().Get(key), "%d", &v)
	return v
}

func openBrowser(url string) {
	time.Sleep(200 * time.Millisecond)
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	}
	if cmd != nil {
		cmd.Run()
	}
}
