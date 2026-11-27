package snapshot_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"duckfam.us/sngl/internal/snapshot"
	"duckfam.us/sngl/ir"
)

// A preview runs no build.Refuse, so a call its page's JavaScript cannot make
// reaches the translator, which declines it.
func TestPreviewDeclinesAnUnemittedIntrinsic(t *testing.T) {
	src := filepath.Join(t.TempDir(), "grpc.sngl")
	if err := os.WriteFile(src, []byte(`import async "sngl:async"
import grpc "sngl:grpc"
import ui "sngl:ui"

#[grpc.message("shop.Get")]
struct Get {
    #[grpc.field(1)]
    id int = 0
}

var title = "-"

ui.window {
    ui.button(text=title, @click {
        match var b = grpc.call[Get, Get](server="localhost:50051", method="/shop.Shop/Get", request={id=1}) {
            ok { title = "{b.id}" }
            failed { title = b.code }
        }
    })
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"none", "go"} {
		t.Run(lang, func(t *testing.T) {
			_, err := snapshot.CompilePreviewHTML(src, "html", lang)
			if !ir.IsDeclined(err) || !strings.Contains(err.Error(), "intrinsic grpc.call") {
				t.Fatalf("CompilePreviewHTML(html, %s) = %v; want a decline", lang, err)
			}
		})
	}
}
