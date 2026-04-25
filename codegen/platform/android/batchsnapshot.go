package android

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
)

// batchSnapshotPackage is the manifest package used for the bundled snapshot
// app. Activities live in this package; per-doc Composables live in
// sub-packages (see docPlan.subPackage).
const batchSnapshotPackage = "sngl.snapshot.app"

var _ codegen.BatchSnapshotter = (*Generator)(nil)

// BatchSnapshot implements codegen.BatchSnapshotter. It bundles every doc into
// a single APK with one Activity per doc, builds and installs once, then
// launches each Activity in turn to capture its screenshot. This amortises
// the dominant kotlinc + install cost across all docs in the batch.
func (g *Generator) BatchSnapshot(docs []codegen.BatchDoc, width, height int) (map[string][]byte, error) {
	if len(docs) == 0 {
		return map[string][]byte{}, nil
	}
	if len(docs) == 1 {
		png, err := g.Snapshot(docs[0].Pkg, docs[0].Lang, width, height)
		if err != nil {
			return nil, err
		}
		return map[string][]byte{docs[0].ID: png}, nil
	}

	adb, err := androidTool("adb")
	if err != nil {
		return nil, fmt.Errorf("adb not found — Android SDK required for screenshots")
	}
	if err := ensureDevice(); err != nil {
		return nil, err
	}

	tmpDir, err := os.MkdirTemp("", "sngl-android-batchsnap-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	plans, err := buildDocPlans(docs)
	if err != nil {
		return nil, err
	}

	// Per-doc codegen: emit MainScreen.kt for each doc into its own
	// sub-package (avoids collisions on MainScreen / structs / enums /
	// ErrorEvent across docs). Flatten filenames so directBuild's flat
	// `*.kt` glob picks them all up; package declared inside the file
	// doesn't need a matching directory.
	manifestActs := make([]ManifestActivity, 0, len(plans))
	for i, p := range plans {
		screenSrc, err := generateDocMainScreen(g, p)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.doc.ID, err)
		}
		screenPath := filepath.Join(tmpDir, fmt.Sprintf("MainScreen_%s.kt", p.sanitizedID))
		if err := os.WriteFile(screenPath, screenSrc, 0o644); err != nil {
			return nil, fmt.Errorf("writing %s: %w", screenPath, err)
		}

		actSrc := renderBatchActivity(batchSnapshotPackage, p)
		actPath := filepath.Join(tmpDir, p.activityName+".kt")
		if err := os.WriteFile(actPath, []byte(actSrc), 0o644); err != nil {
			return nil, fmt.Errorf("writing %s: %w", actPath, err)
		}

		manifestActs = append(manifestActs, ManifestActivity{
			Name:       p.activityName,
			IsLauncher: i == 0,
		})
	}

	// Base scaffold: Theme.kt + manifest with all activities + values resources.
	baseCfg := Config{Package: batchSnapshotPackage, Gradle: false}.withDefaults()
	for _, f := range directBuildFilesWithActivities(baseCfg, manifestActs) {
		path := filepath.Join(tmpDir, f.Name)
		os.MkdirAll(filepath.Dir(path), 0o755)
		out, err := os.Create(path)
		if err != nil {
			return nil, fmt.Errorf("creating %s: %w", path, err)
		}
		_, writeErr := f.WriteTo(out)
		out.Close()
		if errors.Is(writeErr, codegen.ErrSkip) {
			os.Remove(path)
			continue
		}
		if writeErr != nil {
			return nil, fmt.Errorf("writing %s: %w", path, writeErr)
		}
	}

	apk, err := g.Build(tmpDir, map[string]string{
		"package": batchSnapshotPackage,
		"gradle":  "false",
	})
	if err != nil {
		return nil, fmt.Errorf("building APK: %w", err)
	}

	if err := adbInstall(apk); err != nil {
		return nil, err
	}
	defer func() {
		exec.Command(adb, "shell", "am", "force-stop", batchSnapshotPackage).Run()
		exec.Command(adb, "uninstall", batchSnapshotPackage).Run()
	}()

	results := make(map[string][]byte, len(plans))
	var captureErrs []error
	for _, p := range plans {
		// Reset process state so timers/coroutines from the previous doc
		// don't bleed into this one.
		exec.Command(adb, "shell", "am", "force-stop", batchSnapshotPackage).Run()
		fqcn := batchSnapshotPackage + "/." + p.activityName
		png, err := launchAndCapture(adb, batchSnapshotPackage, fqcn)
		if err != nil {
			slog.Warn("batch snapshot capture failed", "id", p.doc.ID, "err", err)
			captureErrs = append(captureErrs, fmt.Errorf("%s: %w", p.doc.ID, err))
			continue
		}
		results[p.doc.ID] = png
	}

	if len(results) == 0 && len(captureErrs) > 0 {
		return nil, errors.Join(captureErrs...)
	}
	if len(captureErrs) > 0 {
		slog.Warn("batch snapshot completed with partial failures",
			"failed", len(captureErrs), "ok", len(results))
	}
	return results, nil
}

// docPlan captures the derived names used to emit and launch a single doc
// inside the bundled snapshot APK.
type docPlan struct {
	doc          codegen.BatchDoc
	sanitizedID  string // Kotlin-safe fragment derived from doc.ID
	subPackage   string // e.g. "sngl.snapshot.app.s_home"
	screenAlias  string // import alias for the doc's MainScreen
	activityName string // e.g. "Snap_home_Activity"
}

func buildDocPlans(docs []codegen.BatchDoc) ([]docPlan, error) {
	plans := make([]docPlan, 0, len(docs))
	seen := map[string]int{}
	for _, d := range docs {
		base := sanitizeDocID(d.ID)
		if base == "" {
			return nil, fmt.Errorf("empty doc id")
		}
		s := base
		if c := seen[base]; c > 0 {
			s = fmt.Sprintf("%s_%d", base, c)
		}
		seen[base]++
		plans = append(plans, docPlan{
			doc:          d,
			sanitizedID:  s,
			subPackage:   batchSnapshotPackage + ".s_" + s,
			screenAlias:  "Screen_" + s,
			activityName: "Snap_" + s + "_Activity",
		})
	}
	return plans, nil
}

// generateDocMainScreen invokes the standard codegen pipeline for a single
// doc with main=false and a per-doc package, returning the MainScreen.kt
// bytes.
func generateDocMainScreen(g *Generator, p docPlan) ([]byte, error) {
	resp, err := g.Generate(&codegen.Request{
		Pkg:  p.doc.Pkg,
		Lang: p.doc.Lang,
		Options: map[string]string{
			"main":    "false",
			"package": p.subPackage,
			"gradle":  "false",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("generating android code: %w", err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("generating android code: %s", resp.Error)
	}
	for _, f := range resp.Files {
		if f.Name != "MainScreen.kt" {
			continue
		}
		var buf bytes.Buffer
		if _, err := f.WriteTo(&buf); err != nil {
			return nil, fmt.Errorf("writing MainScreen.kt: %w", err)
		}
		return buf.Bytes(), nil
	}
	return nil, fmt.Errorf("codegen produced no MainScreen.kt")
}

// renderBatchActivity produces the wrapper Activity source for one doc.
// Each Activity lives in the base manifest package and imports the doc's
// MainScreen from its sub-package under a unique alias.
func renderBatchActivity(basePkg string, p docPlan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n\n", basePkg)
	b.WriteString("import android.os.Bundle\n")
	b.WriteString("import androidx.activity.ComponentActivity\n")
	b.WriteString("import androidx.activity.compose.setContent\n")
	b.WriteString("import androidx.activity.enableEdgeToEdge\n")
	fmt.Fprintf(&b, "import %s.ui.theme.AppTheme\n", basePkg)
	fmt.Fprintf(&b, "import %s.MainScreen as %s\n\n", p.subPackage, p.screenAlias)
	fmt.Fprintf(&b, "class %s : ComponentActivity() {\n", p.activityName)
	b.WriteString("    override fun onCreate(savedInstanceState: Bundle?) {\n")
	b.WriteString("        super.onCreate(savedInstanceState)\n")
	b.WriteString("        enableEdgeToEdge()\n")
	b.WriteString("        setContent {\n")
	b.WriteString("            AppTheme {\n")
	fmt.Fprintf(&b, "                %s()\n", p.screenAlias)
	b.WriteString("            }\n")
	b.WriteString("        }\n")
	b.WriteString("    }\n")
	b.WriteString("}\n")
	return b.String()
}

// sanitizeDocID converts an arbitrary doc identifier into a Kotlin-safe
// identifier fragment: ASCII letters/digits/underscores only, with a leading
// underscore if the source begins with a digit. Empty input yields "doc".
func sanitizeDocID(id string) string {
	if id == "" {
		return "doc"
	}
	var b strings.Builder
	for i, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			if i == 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	s := b.String()
	if s == "" {
		return "doc"
	}
	return s
}
