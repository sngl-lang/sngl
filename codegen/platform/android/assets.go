package android

import (
	_ "embed"
	"os"
	"path"
	"strings"

	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
)

// assetsKt is SnglAssets and SnglAssetImage, which read the pictures the
// program carries out of the app's assets: the source `android:SnglAssetImage`
// names, and what the canvas's image shim paints from.
//
//go:embed assets.kt
var assetsKt string

const assetImageIntrinsic = intrinsicNS + "SnglAssetImage"

// usesAssets reports whether the program reads a picture out of the app's
// assets: an image of a file, or a drawing, whose image shim can.
func usesAssets(pkg *ir.Package) bool {
	found := false
	_ = ir.Walk(pkg, func(n ir.Node) error {
		if ni, ok := n.(*ir.NodeInst); ok && (ir.IsShapeContainer(ni) || ni.Component != nil && ni.Component.Intrinsic == assetImageIntrinsic) {
			found = true
			return ir.SkipAll
		}
		return nil
	})
	return found
}

// emitAssetsFile writes SnglAssets beside MainScreen.kt, and the files the
// program carries into the app's assets at the paths it names them by.
func emitAssetsFile(req *codegen.Request, sink codegen.Sink, mainScreen, pkg string, assetsDir string) error {
	if !usesAssets(req.Pkg) {
		return nil
	}
	src := strings.Replace(assetsKt, "package PACKAGE", "package "+pkg, 1)
	if err := writeAndroidFile(sink, path.Join(path.Dir(mainScreen), "SnglAssets.kt"), []byte(src)); err != nil {
		return err
	}
	if assetsDir == "" {
		return nil
	}
	for _, fa := range req.FileAssets {
		data := fa.Data
		if data == nil {
			d, err := os.ReadFile(fa.SrcPath)
			if err != nil {
				return err
			}
			data = d
		}
		if err := writeAndroidFile(sink, path.Join(assetsDir, fa.OutPath), data); err != nil {
			return err
		}
	}
	return nil
}
