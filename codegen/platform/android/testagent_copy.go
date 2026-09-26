package android

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"git.duckfam.us/jonathan/sngl/codegen"
)

// testAgentDir is where the generated project carries pkg/kotlin/testagent,
// so settings.gradle.kts names it relatively and the project does not depend
// on where the checkout that generated it lives.
const testAgentDir = "testagent"

func copyTestAgent(sink codegen.Sink, src string) error {
	copyFile := func(rel string) error {
		data, err := os.ReadFile(filepath.Join(src, rel))
		if err != nil {
			return fmt.Errorf("android testagent: %w", err)
		}
		return writeAndroidFile(sink, testAgentDir+"/"+filepath.ToSlash(rel), data)
	}
	for _, f := range []string{"build.gradle.kts", "settings.gradle.kts"} {
		if err := copyFile(f); err != nil {
			return err
		}
	}
	main := filepath.Join(src, "src", "main")
	return filepath.WalkDir(main, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		return copyFile(rel)
	})
}
