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

	"sort"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/checker"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/optimize"
	"github.com/calico32/kdl-go"
	"github.com/fsnotify/fsnotify"
	"github.com/google/cel-go/cel"
	"github.com/spf13/cobra"
)

var previewCmd = &cobra.Command{
	Use:   "preview [file.sngl.kdl]",
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
	content    []byte        // latest compiled HTML
	doc        *ast.Document // latest parsed AST
	clientsMu  sync.Mutex
	clients    map[chan struct{}]struct{}

	// Schema from stdlib for showing all possible properties
	schemas     checker.SchemaRegistry
	styleNames  []string // all known style property names, sorted
	styleSchema map[string]checker.StylePropSchema
}

func runPreview(cmd *cobra.Command, args []string) error {
	port, _ := cmd.Flags().GetInt("port")
	autoOpen, _ := cmd.Flags().GetBool("open")

	sourceFile, err := filepath.Abs(args[0])
	if err != nil {
		return err
	}

	schemas, styleProps, _, err := checker.LoadStdlib()
	if err != nil {
		return fmt.Errorf("loading stdlib: %w", err)
	}
	var styleNames []string
	for name := range styleProps {
		styleNames = append(styleNames, name)
	}
	sort.Strings(styleNames)

	s := &previewServer{
		sourceFile:  sourceFile,
		sourceDir:   filepath.Dir(sourceFile),
		clients:     make(map[chan struct{}]struct{}),
		schemas:     schemas,
		styleNames:  styleNames,
		styleSchema: styleProps,
	}

	// Initial parse to determine default target
	if err := s.recompile(); err != nil {
		return fmt.Errorf("initial compile: %w", err)
	}

	// Start file watcher
	go s.watch()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleWrapper)
	mux.HandleFunc("GET /preview", s.handlePreview)
	mux.HandleFunc("GET /events", s.handleEvents)
	mux.HandleFunc("GET /targets", s.handleTargets)
	mux.HandleFunc("GET /switch", s.handleSwitch)
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

	if err := checker.Check(doc, s.sourceDir); err != nil {
		return err
	}

	s.mu.Lock()

	// Resolve active target if not set
	if s.activeLang == "" || s.activePlat == "" {
		if len(doc.Outputs) > 0 {
			s.activeLang = doc.Outputs[0].Lang
			s.activePlat = doc.Outputs[0].Platform
		} else {
			s.activeLang = "js"
			s.activePlat = "html"
		}
	}

	activeLang := s.activeLang
	activePlat := s.activePlat
	s.doc = doc
	s.mu.Unlock()

	// Clone for optimization (mutates in-place)
	previewDoc := doc.Clone()
	optimize.Optimize(previewDoc, optimize.Config{
		Platform: activePlat,
		Language: activeLang,
	})

	// Always generate HTML for preview
	jsLang := codegen.LookupLang("js")
	htmlPlat := codegen.LookupPlatform("html")
	if jsLang == nil || htmlPlat == nil {
		return fmt.Errorf("html/js codegen not registered")
	}

	resp, err := htmlPlat.Generate(&codegen.Request{
		Doc:     previewDoc,
		Lang:    jsLang,
		Options: map[string]string{"preview": "true"},
	})
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("%s", resp.Error)
	}

	html := resp.Files[0].Content

	// Inject preview CSS from active platform if it implements PreviewStyler
	if activePlat != "html" {
		plat := codegen.LookupPlatform(activePlat)
		if styler, ok := plat.(codegen.PreviewStyler); ok {
			css := styler.PreviewCSS()
			injection := fmt.Sprintf("<style>%s</style>\n</head>", css)
			html = []byte(strings.Replace(string(html), "</head>", injection, 1))
		}
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
	// Inject live-reload + selection script before </body>
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

	// Send initial connected event
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

	// From document outputs
	if doc != nil {
		for _, o := range doc.Outputs {
			key := o.Platform + "/" + o.Lang
			if seen[key] {
				continue
			}
			seen[key] = true
			targets = append(targets, target{
				Platform: o.Platform,
				Lang:     o.Lang,
				Active:   o.Platform == activePlat && o.Lang == activeLang,
			})
		}
	}

	// From registry
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

// --- Node API for visual editor ---

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
	CEL     string   `json:"cel,omitempty"`
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
	if e.CEL != "" {
		p.CEL = e.CEL
	} else {
		p.Literal = e.Literal
	}
	return p
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

	if doc == nil || doc.App == nil {
		http.Error(w, "no document", http.StatusNotFound)
		return
	}

	vn := findNode(doc.App.Children, line, col)
	if vn == nil {
		// Also search component bodies
		for _, comp := range doc.Components {
			vn = findNode(comp.Body, line, col)
			if vn != nil {
				break
			}
		}
	}
	if vn == nil {
		http.Error(w, "node not found", http.StatusNotFound)
		return
	}

	resp := nodeResponse{
		Component: vn.Component,
		Pos:       nodePos{Line: vn.Pos.Line, Col: vn.Pos.Column},
		Props:     make(map[string]propJSON),
		Styles:    make(map[string]propJSON),
	}

	// Look up the component schema for all possible props/events
	schema := s.schemas[vn.Component]

	// Add all schema props (unset ones first, then overwrite with set ones)
	if schema != nil {
		for name, ps := range schema.Props {
			resp.Props[name] = propJSON{
				Type: celTypeToString(ps.Type),
				Enum: ps.Enum,
			}
		}
	}
	// Overwrite with actually-set props
	for k, v := range vn.Props {
		typeName := ""
		var enum []string
		if schema != nil {
			if ps, ok := schema.Props[k]; ok {
				typeName = celTypeToString(ps.Type)
				enum = ps.Enum
			}
		}
		resp.Props[k] = exprToPropJSON(v, typeName, enum)
	}

	// Add all known style properties (unset first, then overwrite)
	for _, name := range s.styleNames {
		sp := s.styleSchema[name]
		resp.Styles[name] = propJSON{
			Type: celTypeToString(sp.Type),
			Enum: sp.Enum,
		}
	}
	for k, v := range vn.StyleAttrs {
		sp := s.styleSchema[k]
		resp.Styles[k] = exprToPropJSON(v, celTypeToString(sp.Type), sp.Enum)
	}
	for k, v := range vn.StyleBlock {
		sp := s.styleSchema[k]
		resp.Styles[k] = exprToPropJSON(v, celTypeToString(sp.Type), sp.Enum)
	}

	// Add all schema events (unset first, then mark set ones)
	if schema != nil {
		for name := range schema.Events {
			resp.Events = append(resp.Events, eventJSON{Name: name})
		}
	}
	// Mark set events
	for i, ev := range resp.Events {
		if _, ok := vn.Events[ev.Name]; ok {
			resp.Events[i].Set = true
		}
	}
	// Add any events set on the node that aren't in the schema
	for k := range vn.Events {
		found := false
		for _, ev := range resp.Events {
			if ev.Name == k {
				found = true
				break
			}
		}
		if !found {
			resp.Events = append(resp.Events, eventJSON{Name: k, Set: true})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func celTypeToString(t *cel.Type) string {
	if t == nil {
		return "dyn"
	}
	s := t.String()
	switch s {
	case "string":
		return "string"
	case "int":
		return "int"
	case "double":
		return "float"
	case "bool":
		return "bool"
	case "dyn":
		return "dyn"
	case "sngl.Color":
		return "color"
	case "sngl.Date":
		return "date"
	case "sngl.Time":
		return "time"
	case "sngl.DateTime":
		return "date-time"
	case "sngl.Duration":
		return "duration"
	case "sngl.URL":
		return "url"
	case "sngl.Email":
		return "email"
	case "sngl.UUID":
		return "uuid"
	case "sngl.Regex":
		return "regex"
	case "sngl.Base64":
		return "base64"
	case "sngl.IPV4":
		return "ipv4"
	case "sngl.IPV6":
		return "ipv6"
	case "sngl.Hostname":
		return "hostname"
	case "sngl.IDNEmail":
		return "idn-email"
	case "sngl.IDNHostname":
		return "idn-hostname"
	case "sngl.IRL":
		return "irl"
	case "sngl.IRLReference":
		return "irl-reference"
	case "sngl.URLReference":
		return "url-reference"
	case "sngl.URLTemplate":
		return "url-template"
	case "sngl.Currency":
		return "currency"
	case "sngl.Country2":
		return "country-2"
	case "sngl.Country3":
		return "country-3"
	case "sngl.CountrySubdivision":
		return "country-subdivision"
	case "sngl.Decimal":
		return "decimal"
	default:
		return s
	}
}

func findNode(nodes []*ast.VisualNode, line, col int) *ast.VisualNode {
	for _, vn := range nodes {
		if vn.Pos.Line == line && vn.Pos.Column == col {
			return vn
		}
		if found := findNode(vn.Children, line, col); found != nil {
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

	// Parse KDL source
	f, err := os.Open(s.sourceFile)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	kdlDoc, err := kdl.Parse(f)
	f.Close()
	if err != nil {
		http.Error(w, "KDL parse: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Find the KDL node at the given position
	kdlNode := findKDLNode(kdlDoc.Nodes, line, col)
	if kdlNode == nil {
		http.Error(w, "KDL node not found at position", http.StatusNotFound)
		return
	}

	// Update properties
	for k, v := range body.Props {
		kdlNode.RemoveProperty(k)
		kdlNode.AddProperty(k, parseKDLValue(v))
	}
	// Update style properties (style.X=Y form)
	for k, v := range body.Styles {
		propKey := "style." + k
		kdlNode.RemoveProperty(propKey)
		kdlNode.AddProperty(propKey, parseKDLValue(v))
	}

	// Emit back to file
	out, err := os.Create(s.sourceFile)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	err = kdl.Emit(kdlDoc, out)
	out.Close()
	if err != nil {
		http.Error(w, "KDL emit: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// fsnotify will pick up the write → recompile → SSE reload
	w.WriteHeader(http.StatusOK)
}

func findKDLNode(nodes []*kdl.Node, line, col int) *kdl.Node {
	for _, n := range nodes {
		loc := n.Location()
		if loc.Line == line && loc.Column == col {
			return n
		}
		if ch := n.Children(); ch != nil {
			if found := findKDLNode(ch.Nodes, line, col); found != nil {
				return found
			}
		}
	}
	return nil
}

func parseKDLValue(s string) kdl.Value {
	// Try integer
	var i int
	if _, err := fmt.Sscanf(s, "%d", &i); err == nil && fmt.Sprintf("%d", i) == s {
		return kdl.NewInt(i)
	}
	// Try float
	var f float64
	if _, err := fmt.Sscanf(s, "%g", &f); err == nil {
		return kdl.NewFloat(f)
	}
	// Try bool
	if s == "true" {
		return kdl.NewBool(true)
	}
	if s == "false" {
		return kdl.NewBool(false)
	}
	return kdl.NewString(s)
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
