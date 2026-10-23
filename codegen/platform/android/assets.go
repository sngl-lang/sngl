package android

import (
	_ "embed"
	"os"
	"path"
	"strings"

	"duckfam.us/sngl/codegen"
	"duckfam.us/sngl/ir"
)

// assetsKt is SnglAssets and SnglAssetImage, which read the pictures the
// program carries out of the app's assets: the source `android:SnglAssetImage`
// names, and what the canvas's image shim paints from.
//
//go:embed assets.kt
var assetsKt string

const (
	assetImageIntrinsic  = intrinsicNS + "SnglAssetImage"
	remoteImageIntrinsic = intrinsicNS + "SnglRemoteImage"
	imageIntrinsic       = intrinsicNS + "SnglImage"
)

// usesAssets reports whether the program shows a picture through SnglAssets:
// an image, a file's or a URL's picture, or a drawing, whose image shim
// reads the app's assets.
func usesAssets(pkg *ir.Package) bool {
	return reaches(pkg, func(n ir.Node) bool {
		ni, ok := n.(*ir.NodeInst)
		if !ok {
			return false
		}
		if ir.IsShapeContainer(ni) {
			return true
		}
		if ni.Component == nil {
			return false
		}
		switch ni.Component.Intrinsic {
		case assetImageIntrinsic, remoteImageIntrinsic, imageIntrinsic:
			return true
		}
		return false
	})
}

// reaches reports whether any node of pkg satisfies is.
func reaches(pkg *ir.Package, is func(ir.Node) bool) bool {
	found := false
	_ = ir.Walk(pkg, func(n ir.Node) error {
		if is(n) {
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
