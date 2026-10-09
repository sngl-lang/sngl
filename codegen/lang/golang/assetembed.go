package golang

import (
	"os"

	"git.duckfam.us/jonathan/sngl/codegen"
)

// SnglImageImportPath is the runtime a Go target's image sources load through.
const SnglImageImportPath = "git.duckfam.us/jonathan/sngl/pkg/go/snglimage"

// EmitAssetEmbed writes the `file:` assets a build copied beside the program,
// and `assets.go`, which embeds them and hands them to snglimage, so a file
// an image names is carried in the binary at the path the build gave it.
func EmitAssetEmbed(sink codegen.Sink, pkgName string, assets []codegen.FileAsset) error {
	if len(assets) == 0 {
		return nil
	}
	for _, fa := range assets {
		data := fa.Data
		if data == nil {
			d, err := os.ReadFile(fa.SrcPath)
			if err != nil {
				return err
			}
			data = d
		}
		if err := writeSinkFile(sink, fa.OutPath, data); err != nil {
			return err
		}
	}
	return writeSinkFile(sink, "assets.go", []byte("package "+pkgName+"\n\n"+
		"import (\n"+
		"\t\"embed\"\n\n"+
		"\tsnglimage \""+SnglImageImportPath+"\"\n"+
		")\n\n"+
		"//go:embed assets\n"+
		"var snglAssets embed.FS\n\n"+
		"func init() { snglimage.Embed(snglAssets) }\n"))
}
