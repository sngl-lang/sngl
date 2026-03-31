package android

import (
	"fmt"
	"os"
	"os/exec"
)

// gomobileBind runs "gomobile bind" on the Go module at goLibDir, producing
// an AAR at aarPath that exposes the module's exported functions to Kotlin.
func gomobileBind(goLibDir, aarPath string) error {
	gomobile, err := exec.LookPath("gomobile")
	if err != nil {
		return fmt.Errorf("gomobile not found in PATH (install with: go install golang.org/x/mobile/cmd/gomobile@latest)")
	}

	fmt.Fprintln(os.Stderr, "sngl: running gomobile bind...")
	bind := exec.Command(gomobile, "bind",
		"-target", "android",
		"-o", aarPath,
		".",
	)
	bind.Dir = goLibDir
	bind.Stdout = os.Stderr
	bind.Stderr = os.Stderr
	if err := bind.Run(); err != nil {
		return fmt.Errorf("gomobile bind failed: %w", err)
	}
	return nil
}
