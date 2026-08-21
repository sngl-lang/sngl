package android

import (
	"strings"
	"testing"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/internal/checker"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

func TestSanitizeDocID(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"home", "home"},
		{"Home", "Home"},
		{"home_v2", "home_v2"},
		{"home-page", "home_page"},
		{"home.page", "home_page"},
		{"home/page", "home_page"},
		{"123abc", "_123abc"},
		{"a b c", "a_b_c"},
		{"", "doc"},
		{"!!", "__"},
	}
	for _, c := range cases {
		if got := sanitizeDocID(c.in); got != c.want {
			t.Errorf("sanitizeDocID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBuildDocPlansCollision(t *testing.T) {
	docs := []codegen.BatchDoc{
		{ID: "home"},
		{ID: "home"},      // exact dup
		{ID: "home-page"}, // sanitizes to "home_page"
		{ID: "home/page"}, // sanitizes to "home_page" — collision
	}
	plans, err := buildDocPlans(docs)
	if err != nil {
		t.Fatalf("buildDocPlans: %v", err)
	}
	if len(plans) != 4 {
		t.Fatalf("want 4 plans, got %d", len(plans))
	}
	seen := map[string]bool{}
	for _, p := range plans {
		if seen[p.subPackage] {
			t.Errorf("duplicate subPackage %q", p.subPackage)
		}
		if seen[p.activityName] {
			t.Errorf("duplicate activityName %q", p.activityName)
		}
		seen[p.subPackage] = true
		seen[p.activityName] = true
	}
}

// TestBatchCodegen drives the codegen-layer pieces of BatchSnapshot
// (sub-package emission, activity wrapper, manifest with N activities)
// without involving adb / kotlinc. It's the closest we can get to an
// end-to-end check without a connected device.
func TestBatchCodegen(t *testing.T) {
	const src1 = `import . "sngl://std"
output { android }
` + "`" + `Hello A` + "`" + `
`
	const src2 = `import . "sngl://std"
output { android }
` + "`" + `Hello B` + "`" + `
`

	check := func(name, body string) *codegen.BatchDoc {
		t.Helper()
		doc, err := parser.Parse(name, []byte(body))
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		pkg, diags := checker.Check(doc, &checker.Config{IsMain: true})
		for _, d := range diags {
			if d.Error() != "" && d.Error()[0] != ' ' {
				// only inspect on actual error
			}
		}
		_ = diags
		return &codegen.BatchDoc{
			ID:   strings.TrimSuffix(name, ".sngl"),
			Pkg:  pkg,
			Lang: codegen.LookupLang("kotlin"),
		}
	}

	d1 := check("doc_a.sngl", src1)
	d2 := check("doc_b.sngl", src2)

	plans, err := buildDocPlans([]codegen.BatchDoc{*d1, *d2})
	if err != nil {
		t.Fatalf("buildDocPlans: %v", err)
	}
	if len(plans) != 2 {
		t.Fatalf("want 2 plans, got %d", len(plans))
	}

	g := &Generator{}

	// Each doc should produce a MainScreen with its own sub-package declaration.
	for _, p := range plans {
		src, err := generateDocMainScreen(g, p)
		if err != nil {
			t.Fatalf("%s: generateDocMainScreen: %v", p.doc.ID, err)
		}
		want := "package " + p.subPackage
		if !strings.Contains(string(src), want) {
			t.Errorf("%s: MainScreen missing %q\n--- src:\n%s", p.doc.ID, want, src)
		}
		if !strings.Contains(string(src), "fun MainScreen()") {
			t.Errorf("%s: MainScreen missing fun MainScreen()", p.doc.ID)
		}
	}

	// Activity wrappers should sit in the base package and import the
	// per-doc MainScreen under a unique alias.
	a1 := renderBatchActivity(batchSnapshotPackage, plans[0])
	if !strings.Contains(a1, "package "+batchSnapshotPackage+"\n") {
		t.Errorf("activity wrong package:\n%s", a1)
	}
	if !strings.Contains(a1, "import "+plans[0].subPackage+".MainScreen as "+plans[0].screenAlias) {
		t.Errorf("activity missing aliased import:\n%s", a1)
	}
	if !strings.Contains(a1, "class "+plans[0].activityName+" : ComponentActivity") {
		t.Errorf("activity missing class declaration:\n%s", a1)
	}

	// Manifest scaffold with two activities — exactly one launcher, both exported.
	acts := []ManifestActivity{
		{Name: plans[0].activityName, IsLauncher: true},
		{Name: plans[1].activityName, IsLauncher: false},
	}
	noGradle := false
	files := directBuildFilesWithActivities(Config{Package: batchSnapshotPackage, Gradle: &noGradle}.withDefaults(), acts)

	var manifestBody string
	var sawMainActivityKt bool
	for _, f := range files {
		if f.Name == "MainActivity.kt" {
			sawMainActivityKt = true
		}
		if f.Name == "AndroidManifest.xml" {
			var sb strings.Builder
			f.WriteTo(&sb)
			manifestBody = sb.String()
		}
	}
	if sawMainActivityKt {
		t.Errorf("batch scaffold should omit MainActivity.kt")
	}
	if manifestBody == "" {
		t.Fatalf("AndroidManifest.xml not emitted")
	}
	if !strings.Contains(manifestBody, `android:name=".`+plans[0].activityName+`"`) {
		t.Errorf("manifest missing first activity:\n%s", manifestBody)
	}
	if !strings.Contains(manifestBody, `android:name=".`+plans[1].activityName+`"`) {
		t.Errorf("manifest missing second activity:\n%s", manifestBody)
	}
	if got := strings.Count(manifestBody, "android.intent.category.LAUNCHER"); got != 1 {
		t.Errorf("want exactly 1 LAUNCHER category, got %d:\n%s", got, manifestBody)
	}
	if got := strings.Count(manifestBody, `android:exported="true"`); got != 2 {
		t.Errorf("want both activities exported=true, got %d:\n%s", got, manifestBody)
	}
}

// TestSingleActivityManifestUnchanged guards against accidentally breaking
// the non-batch code path: directBuildFiles should still produce the
// canonical single-MainActivity manifest.
func TestSingleActivityManifestUnchanged(t *testing.T) {
	noGradle2 := false
	files := directBuildFiles(Config{Package: "test.sngl.app", Gradle: &noGradle2}.withDefaults(), false, false)
	var manifest string
	var sawMainActivity bool
	for _, f := range files {
		if f.Name == "AndroidManifest.xml" {
			var sb strings.Builder
			f.WriteTo(&sb)
			manifest = sb.String()
		}
		if f.Name == "MainActivity.kt" {
			sawMainActivity = true
		}
	}
	if !sawMainActivity {
		t.Errorf("default scaffold should still emit MainActivity.kt")
	}
	if !strings.Contains(manifest, `android:name=".MainActivity"`) {
		t.Errorf("default manifest missing MainActivity:\n%s", manifest)
	}
	if strings.Count(manifest, "<activity") != 1 {
		t.Errorf("default manifest should have exactly one <activity:\n%s", manifest)
	}
	if strings.Count(manifest, "android.intent.category.LAUNCHER") != 1 {
		t.Errorf("default manifest missing LAUNCHER category:\n%s", manifest)
	}
}
