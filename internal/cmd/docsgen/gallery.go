package main

import (
	"html/template"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/snapshot"
)

type componentPageData struct {
	Name       string
	Doc        string
	Props      []propData
	Events     []eventData
	Children   string // "none", "one", "many"
	Example    string // source code
	HasExample bool
	Tier       string
}

type propData struct {
	Name string
	Type string
	Enum []string
	Doc  string
}

type eventData struct {
	Name        string
	PayloadType string
}

// tierOrder defines the canonical ordering and grouping of tiers.
var tierOrder = []string{
	"Core",
	"Tier 1: Core Input",
	"Tier 2: Feedback & Navigation",
	"Tier 3: Overlays & Layout",
	"Tier 4: Data & Desktop",
	"Tier 5: Mobile & Specialized",
}

// componentTiers maps component names to their tier based on the stdlib file.
// This is populated by parsing the tier comments from components.sngl.
func assignTiers(registry checker.SchemaRegistry) map[string]string {
	tiers := map[string]string{}
	// We parse the stdlib file to get tier boundaries. The components.sngl
	// file uses "// --- Tier Name ---" comment lines to separate tiers.
	// Components before the first tier marker are "Core".
	data, err := os.ReadFile("internal/checker/stdlib/components.sngl")
	if err != nil {
		// Fall back: all components are "Core".
		for name := range registry {
			tiers[name] = "Core"
		}
		return tiers
	}
	lines := strings.Split(string(data), "\n")

	currentTier := "Core"
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "// ---") && strings.HasSuffix(trimmed, "---") {
			tierName := strings.TrimPrefix(trimmed, "// ---")
			tierName = strings.TrimSuffix(tierName, "---")
			tierName = strings.TrimSpace(tierName)
			if tierName != "" {
				currentTier = tierName
			}
			continue
		}
		if strings.HasPrefix(trimmed, "component ") {
			parts := strings.Fields(trimmed)
			if len(parts) >= 2 {
				tiers[parts[1]] = currentTier
			}
		}
	}
	return tiers
}

func generateGallery(docsDir, outDir string) {
	registry, _, _, _, _, err := checker.LoadStdlib()
	if err != nil {
		log.Printf("gallery: failed to load stdlib: %v", err)
		return
	}

	tiers := assignTiers(registry)

	// Read component examples from docs/components/.
	examplesDir := filepath.Join(docsDir, "components")
	examples := map[string]string{}
	entries, err := os.ReadDir(examplesDir)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".sngl") {
				continue
			}
			name := strings.TrimSuffix(e.Name(), ".sngl")
			data, err := os.ReadFile(filepath.Join(examplesDir, e.Name()))
			if err != nil {
				continue
			}
			examples[name] = string(data)
		}
	}

	// Build component page data sorted by tier then name.
	var components []componentPageData
	for name, schema := range registry {
		cpd := componentPageData{
			Name:     name,
			Doc:      schema.Doc,
			Children: childPolicyString(schema.Children),
			Tier:     tiers[name],
		}

		// Props sorted by name.
		for pname, pschema := range schema.Props {
			cpd.Props = append(cpd.Props, propData{
				Name: pname,
				Type: pschema.Type.String(),
				Enum: pschema.Enum,
				Doc:  pschema.Doc,
			})
		}
		sort.Slice(cpd.Props, func(i, j int) bool {
			return cpd.Props[i].Name < cpd.Props[j].Name
		})

		// Events sorted by name.
		for ename, payload := range schema.Events {
			cpd.Events = append(cpd.Events, eventData{
				Name:        ename,
				PayloadType: payload,
			})
		}
		sort.Slice(cpd.Events, func(i, j int) bool {
			return cpd.Events[i].Name < cpd.Events[j].Name
		})

		if src, ok := examples[name]; ok {
			cpd.Example = src
			cpd.HasExample = true
		}

		components = append(components, cpd)
	}

	// Sort by tier order, then name.
	tierIdx := map[string]int{}
	for i, t := range tierOrder {
		tierIdx[t] = i
	}
	sort.Slice(components, func(i, j int) bool {
		ti := tierIdx[components[i].Tier]
		tj := tierIdx[components[j].Tier]
		if ti != tj {
			return ti < tj
		}
		return components[i].Name < components[j].Name
	})

	// Create output directories.
	componentsDir := filepath.Join(outDir, "components")
	galleryAssetsDir := filepath.Join(outDir, "assets", "gallery")
	os.MkdirAll(componentsDir, 0o755)
	os.MkdirAll(galleryAssetsDir, 0o755)

	// Generate individual component pages.
	for _, comp := range components {
		if err := generateComponentPage(comp, componentsDir); err != nil {
			log.Printf("gallery: %s: %v", comp.Name, err)
			continue
		}

		// Generate HTML preview and snapshots for components with examples.
		if comp.HasExample {
			generateComponentSnapshots(comp.Name, examplesDir, componentsDir, galleryAssetsDir)
		}

		log.Printf("gallery: %s", comp.Name)
	}

	// Generate index page.
	if err := generateGalleryIndex(components, componentsDir); err != nil {
		log.Printf("gallery: index: %v", err)
	}

	log.Printf("gallery: %d components", len(components))
}

func generateComponentSnapshots(name, examplesDir, componentsDir, galleryAssetsDir string) {
	sourceFile := filepath.Join(examplesDir, name+".sngl")

	// Compile to HTML for interactive iframe preview.
	html, err := snapshot.CompilePreviewHTML(sourceFile, "html", "js")
	if err != nil {
		log.Printf("gallery: %s: compile preview: %v", name, err)
		return
	}
	htmlPath := filepath.Join(componentsDir, name+"-html.html")
	if err := os.WriteFile(htmlPath, html, 0o644); err != nil {
		log.Printf("gallery: %s: write html: %v", name, err)
	}

	// Generate PNG snapshots for other platforms (optional, best-effort).
	for _, platform := range []string{"bubbletea", "fyne"} {
		results, err := snapshot.Generate(snapshot.Config{
			SourceFile: sourceFile,
			Platforms:  []string{platform},
			OutDir:     galleryAssetsDir,
			Width:      800,
			Height:     400,
		})
		if err != nil {
			log.Printf("gallery: %s: snapshot %s: %v (skipping)", name, platform, err)
			continue
		}
		// Rename output to {name}-{platform}.png.
		for _, r := range results {
			target := filepath.Join(galleryAssetsDir, name+"-"+platform+".png")
			if r.Path != target {
				os.Rename(r.Path, target)
			}
		}
	}
}

func childPolicyString(cp checker.ChildPolicy) string {
	switch cp {
	case checker.ChildrenNone:
		return "none"
	case checker.ChildrenOne:
		return "one"
	case checker.ChildrenMany:
		return "many"
	default:
		return "unknown"
	}
}

var componentPageTmpl = template.Must(template.New("component").Funcs(template.FuncMap{
	"joinEnum": func(vals []string) string {
		return strings.Join(vals, ", ")
	},
}).Parse(`<!DOCTYPE html>
<html>
<head>
    <title>{{.Name}} — SNGL Components</title>
    <link rel="stylesheet" href="../assets/style.css">
    <style>
        .component-page { max-width: 900px; margin: 0 auto; padding: 24px; }
        .platform-tabs { display: flex; gap: 4px; margin-bottom: 16px; }
        .platform-tabs button { padding: 8px 16px; border: 1px solid #ddd; background: #f5f5f5; cursor: pointer; border-radius: 4px 4px 0 0; }
        .platform-tabs button.active { background: #fff; border-bottom-color: #fff; font-weight: bold; }
        .preview-frame { border: 1px solid #ddd; border-radius: 0 4px 4px 4px; min-height: 200px; overflow: hidden; }
        .preview-frame iframe { width: 100%; height: 300px; border: none; }
        .preview-frame img { width: 100%; }
        .preview[hidden] { display: none; }
        .props-table { width: 100%; border-collapse: collapse; margin: 16px 0; }
        .props-table th, .props-table td { border: 1px solid #ddd; padding: 8px 12px; text-align: left; }
        .props-table th { background: #f5f5f5; }
        .type-badge { background: #e8f0fe; color: #1967d2; padding: 2px 6px; border-radius: 3px; font-size: 0.85em; }
        .enum-values { color: #666; font-size: 0.85em; }
        pre { background: #f5f5f5; padding: 16px; border-radius: 4px; overflow-x: auto; }
        code { font-family: "JetBrains Mono", "Fira Code", monospace; }
    </style>
</head>
<body>
<div class="component-page">
    <h1>{{.Name}}</h1>
    {{if .Doc}}<p>{{.Doc}}</p>{{end}}

    {{if .HasExample}}
    <h2>Preview</h2>
    <div class="platform-tabs">
        <button class="active" onclick="switchPlatform('html', this)">HTML (Interactive)</button>
        <button onclick="switchPlatform('bubbletea', this)">Bubbletea</button>
        <button onclick="switchPlatform('fyne', this)">Fyne</button>
    </div>
    <div class="preview-frame">
        <div class="preview" data-platform="html">
            <iframe src="{{.Name}}-html.html"></iframe>
        </div>
        <div class="preview" data-platform="bubbletea" hidden>
            <img src="../assets/gallery/{{.Name}}-bubbletea.png" alt="{{.Name}} on Bubbletea">
        </div>
        <div class="preview" data-platform="fyne" hidden>
            <img src="../assets/gallery/{{.Name}}-fyne.png" alt="{{.Name}} on Fyne">
        </div>
    </div>
    {{end}}

    {{if .Props}}
    <h2>Properties</h2>
    <table class="props-table">
        <thead><tr><th>Name</th><th>Type</th><th>Description</th></tr></thead>
        <tbody>
        {{range .Props}}
        <tr>
            <td><code>{{.Name}}</code></td>
            <td><span class="type-badge">{{.Type}}</span>{{if .Enum}} <span class="enum-values">{{joinEnum .Enum}}</span>{{end}}</td>
            <td>{{.Doc}}</td>
        </tr>
        {{end}}
        </tbody>
    </table>
    {{end}}

    {{if .Events}}
    <h2>Events</h2>
    <table class="props-table">
        <thead><tr><th>Name</th><th>Payload</th></tr></thead>
        <tbody>
        {{range .Events}}
        <tr><td><code>{{.Name}}</code></td><td>{{.PayloadType}}</td></tr>
        {{end}}
        </tbody>
    </table>
    {{end}}

    <p><strong>Children:</strong> {{.Children}}</p>

    {{if .HasExample}}
    <h2>Example</h2>
    <pre><code>{{.Example}}</code></pre>
    {{end}}
</div>
<script>
function switchPlatform(platform, btn) {
    document.querySelectorAll('.preview').forEach(p => p.hidden = p.dataset.platform !== platform);
    document.querySelectorAll('.platform-tabs button').forEach(b => b.classList.remove('active'));
    btn.classList.add('active');
}
</script>
</body>
</html>
`))

func generateComponentPage(comp componentPageData, outDir string) error {
	path := filepath.Join(outDir, comp.Name+".html")
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return componentPageTmpl.Execute(f, comp)
}

type galleryIndexData struct {
	Tiers []galleryTier
}

type galleryTier struct {
	Name       string
	Components []componentPageData
}

var galleryIndexTmpl = template.Must(template.New("index").Parse(`<!DOCTYPE html>
<html>
<head>
    <title>Component Gallery — SNGL</title>
    <link rel="stylesheet" href="../assets/style.css">
    <style>
        .gallery { max-width: 900px; margin: 0 auto; padding: 24px; }
        .tier { margin-bottom: 32px; }
        .tier h2 { border-bottom: 1px solid #ddd; padding-bottom: 8px; }
        .component-list { display: grid; grid-template-columns: repeat(auto-fill, minmax(200px, 1fr)); gap: 12px; }
        .component-card { border: 1px solid #ddd; border-radius: 8px; padding: 16px; text-decoration: none; color: inherit; transition: box-shadow 0.2s; }
        .component-card:hover { box-shadow: 0 2px 8px rgba(0,0,0,0.1); }
        .component-card h3 { margin: 0 0 4px 0; }
        .component-card p { margin: 0; font-size: 0.9em; color: #666; }
    </style>
</head>
<body>
<div class="gallery">
    <h1>SNGL Component Gallery</h1>
    <p>Browse all built-in components with API reference and live previews.</p>

    {{range .Tiers}}
    <div class="tier">
        <h2>{{.Name}}</h2>
        <div class="component-list">
            {{range .Components}}
            <a class="component-card" href="{{.Name}}.html">
                <h3>{{.Name}}</h3>
                {{if .Doc}}<p>{{.Doc}}</p>{{end}}
            </a>
            {{end}}
        </div>
    </div>
    {{end}}
</div>
</body>
</html>
`))

func generateGalleryIndex(components []componentPageData, outDir string) error {
	// Group by tier preserving tier order.
	tierMap := map[string][]componentPageData{}
	for _, c := range components {
		tierMap[c.Tier] = append(tierMap[c.Tier], c)
	}

	var tiers []galleryTier
	for _, t := range tierOrder {
		if comps, ok := tierMap[t]; ok {
			tiers = append(tiers, galleryTier{Name: t, Components: comps})
		}
	}

	data := galleryIndexData{Tiers: tiers}
	path := filepath.Join(outDir, "index.html")
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return galleryIndexTmpl.Execute(f, data)
}
