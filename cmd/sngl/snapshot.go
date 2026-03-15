package main

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/internal/snapshot"
	"github.com/spf13/cobra"
)

var snapshotCmd = &cobra.Command{
	Use:   "snapshot [file.sngl]",
	Short: "Generate platform screenshots",
	Args:  cobra.ExactArgs(1),
	RunE:  runSnapshot,
}

func init() {
	snapshotCmd.Flags().StringSlice("platform", nil, "platforms to snapshot (default: all from output block)")
	snapshotCmd.Flags().String("out", "snapshots", "output directory")
	snapshotCmd.Flags().Int("width", 1280, "viewport width")
	snapshotCmd.Flags().Int("height", 720, "viewport height")
}

func runSnapshot(cmd *cobra.Command, args []string) error {
	platforms, _ := cmd.Flags().GetStringSlice("platform")
	outDir, _ := cmd.Flags().GetString("out")
	width, _ := cmd.Flags().GetInt("width")
	height, _ := cmd.Flags().GetInt("height")

	results, err := snapshot.Generate(snapshot.Config{
		SourceFile: args[0],
		Platforms:  platforms,
		Width:      width,
		Height:     height,
		OutDir:     outDir,
	})
	if err != nil {
		return err
	}

	for _, r := range results {
		fmt.Printf("  %s → %s\n", r.Platform, r.Path)
	}
	return nil
}
