package docsite

import (
	"bytes"
	"html/template"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"git.duckfam.us/jonathan/sngl/internal/checker"
)

// ComponentPageData holds all data needed to render a component page.
type ComponentPageData struct {
	Name               string
	Doc                string
	Props              []PropData
	Events             []EventData
	Children           string // "none", "one", "many"
	Example            string // raw source code
	HighlightedExample string // syntax-highlighted HTML
	HasExample         bool
	Tier               string
}

// PropData describes a single component property.
type PropData struct {
	Name string
	Type string
	Enum []string
	Doc  string
}

// EventData describes a single component event.
type EventData struct {
	Name        string
	PayloadType string
}

// TierOrder defines the canonical ordering and grouping of tiers.
var TierOrder = []string{
	"Core",
	"Tier 1: Core Input",
	"Tier 2: Feedback & Navigation",
	"Tier 3: Overlays & Layout",
	"Tier 4: Data & Desktop",
	"Tier 5: Mobile & Specialized",
}

// AssignTiers maps component names to their tier based on the stdlib
// components.sngl file. It reads the embedded stdlib to find tier boundary
// comments.
func AssignTiers(registry checker.SchemaRegistry) map[string]string {
	tiers := map[string]string{}
	data, err := checker.StdlibFS().ReadFile("stdlib/components.sngl")
	if err != nil {
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

// ChildPolicyString converts a ChildPolicy to a human-readable string.
func ChildPolicyString(cp checker.ChildPolicy) string {
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

// BuildComponentData loads the stdlib and builds component page data for all
// components, sorted by tier then name.
func BuildComponentData(examplesDir string) ([]ComponentPageData, error) {
	registry, _, _, _, _, err := checker.LoadStdlib()
	if err != nil {
		return nil, err
	}

	tiers := AssignTiers(registry)

	// Read component examples.
	examples := map[string]string{}
	if examplesDir != "" {
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
	}

	var components []ComponentPageData
	for name, schema := range registry {
		cpd := ComponentPageData{
			Name:     name,
			Doc:      schema.Doc,
			Children: ChildPolicyString(schema.Children),
			Tier:     tiers[name],
		}

		for pname, pschema := range schema.Props {
			cpd.Props = append(cpd.Props, PropData{
				Name: pname,
				Type: pschema.Type.String(),
				Enum: pschema.Enum,
				Doc:  pschema.Doc,
			})
		}
		sort.Slice(cpd.Props, func(i, j int) bool {
			return cpd.Props[i].Name < cpd.Props[j].Name
		})

		for ename, payload := range schema.Events {
			cpd.Events = append(cpd.Events, EventData{
				Name:        ename,
				PayloadType: payload,
			})
		}
		sort.Slice(cpd.Events, func(i, j int) bool {
			return cpd.Events[i].Name < cpd.Events[j].Name
		})

		if src, ok := examples[name]; ok {
			cpd.Example = src
			cpd.HighlightedExample = HighlightSNGL(src)
			cpd.HasExample = true
		}

		components = append(components, cpd)
	}

	// Sort by tier order, then name.
	tierIdx := map[string]int{}
	for i, t := range TierOrder {
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

	return components, nil
}

// GalleryTier groups components by tier for the gallery index.
type GalleryTier struct {
	Name       string
	Components []ComponentPageData
}

// GenerateGallery generates the component gallery HTML pages in outDir.
// docsDir is used to find component example .sngl files (in docsDir/components/).
// snapshotFn, if non-nil, is called for each component with an example to
// generate preview HTML and platform snapshots.
func GenerateGallery(docsDir, outDir string, layout *SiteLayout, snapshotFn func(name, examplesDir, componentsDir, galleryAssetsDir string)) error {
	examplesDir := filepath.Join(docsDir, "components")
	components, err := BuildComponentData(examplesDir)
	if err != nil {
		return err
	}

	componentsDir := filepath.Join(outDir, "components")
	galleryAssetsDir := filepath.Join(outDir, "assets", "gallery")
	os.MkdirAll(componentsDir, 0o755)
	os.MkdirAll(galleryAssetsDir, 0o755)

	for _, comp := range components {
		if err := generateComponentPage(comp, componentsDir, layout); err != nil {
			log.Printf("gallery: %s: %v", comp.Name, err)
			continue
		}

		if comp.HasExample && snapshotFn != nil {
			snapshotFn(comp.Name, examplesDir, componentsDir, galleryAssetsDir)
		}

		log.Printf("gallery: %s", comp.Name)
	}

	if err := generateGalleryIndex(components, componentsDir, layout); err != nil {
		return err
	}

	log.Printf("gallery: %d components", len(components))
	return nil
}

var componentPageTmpl = template.Must(template.New("component").Funcs(template.FuncMap{
	"joinEnum": func(vals []string) string {
		return strings.Join(vals, ", ")
	},
	"safeHTML": func(s string) template.HTML {
		return template.HTML(s)
	},
}).Parse(`<style>
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
</style>
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
    {{.HighlightedExample | safeHTML}}
    {{end}}
</div>
<script>
function switchPlatform(platform, btn) {
    document.querySelectorAll('.preview').forEach(p => p.hidden = p.dataset.platform !== platform);
    document.querySelectorAll('.platform-tabs button').forEach(b => b.classList.remove('active'));
    btn.classList.add('active');
}
</script>
`))

func generateComponentPage(comp ComponentPageData, outDir string, layout *SiteLayout) error {
	var content bytes.Buffer
	if err := componentPageTmpl.Execute(&content, comp); err != nil {
		return err
	}

	var out []byte
	if layout != nil {
		var err error
		out, err = layout.WrapPage(comp.Name+" — Components", template.HTML(content.String()), "components/"+comp.Name+".html")
		if err != nil {
			return err
		}
	} else {
		out = content.Bytes()
	}

	return os.WriteFile(filepath.Join(outDir, comp.Name+".html"), out, 0o644)
}

func generateGalleryIndex(components []ComponentPageData, outDir string, layout *SiteLayout) error {
	tierMap := map[string][]ComponentPageData{}
	for _, c := range components {
		tierMap[c.Tier] = append(tierMap[c.Tier], c)
	}

	var tiers []GalleryTier
	for _, t := range TierOrder {
		if comps, ok := tierMap[t]; ok {
			tiers = append(tiers, GalleryTier{Name: t, Components: comps})
		}
	}

	data := struct{ Tiers []GalleryTier }{Tiers: tiers}

	var content bytes.Buffer
	if err := galleryIndexTmpl.Execute(&content, data); err != nil {
		return err
	}

	var out []byte
	if layout != nil {
		var err error
		out, err = layout.WrapPage("Component Gallery", template.HTML(content.String()), "components/index.html")
		if err != nil {
			return err
		}
	} else {
		out = content.Bytes()
	}

	return os.WriteFile(filepath.Join(outDir, "index.html"), out, 0o644)
}

var galleryIndexTmpl = template.Must(template.New("index").Parse(`<style>
    .tier { margin-bottom: 32px; }
    .tier h2 { border-bottom: 1px solid #ddd; padding-bottom: 8px; }
    .component-list { display: grid; grid-template-columns: repeat(auto-fill, minmax(200px, 1fr)); gap: 12px; }
    .component-card { border: 1px solid #ddd; border-radius: 8px; padding: 16px; text-decoration: none; color: inherit; transition: box-shadow 0.2s; }
    .component-card:hover { box-shadow: 0 2px 8px rgba(0,0,0,0.1); }
    .component-card h3 { margin: 0 0 4px 0; }
    .component-card p { margin: 0; font-size: 0.9em; color: #666; }
</style>

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
`))
