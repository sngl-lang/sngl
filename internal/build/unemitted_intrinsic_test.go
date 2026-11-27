package build_test

import (
	"strings"
	"testing"
	"testing/fstest"

	"duckfam.us/sngl/internal/build"
	"duckfam.us/sngl/ir"
)

// A call of an intrinsic the language it is written in has no implementation
// of is the target declining the program, which the pipeline fuzzer tells
// from a fault by its type (cmd/sngl/testdata/target_declines.txt has the
// messages).
func TestUnemittedIntrinsicIsDeclined(t *testing.T) {
	for _, tc := range []struct{ name, lang, plat, main string }{
		{"time.now", "c", "win32", `import ui "sngl:ui"
import time "sngl:time"

var at = ""

ui.window {
    ui.button(text=at, @click {
        at = string(time.now() == time.now())
    })
}
`},
		{"time.between", "c", "gtk4", `import ui "sngl:ui"
import time "sngl:time"

var a time.datetime = "2024-01-15T10:30:00+05:00"
var b time.datetime = "2024-01-15T10:31:00+05:00"

ui.window {
    ui.text(value=string(int(time.between(a, b) / 1s)))
}
`},
		{"grpc.call", "go", "html", `import async "sngl:async"
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
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fsys := fstest.MapFS{}
			pkg, err := checkKept(t, fsys, tc.main)
			if err != nil {
				t.Fatal(err)
			}
			_, err = build.Emit(pkg, build.Options{Name: "main.sngl", Dir: ".", ProjectFS: fsys, Lang: tc.lang, Platform: tc.plat})
			if !ir.IsDeclined(err) || !strings.Contains(err.Error(), "intrinsic "+tc.name) {
				t.Fatalf("Emit for %s/%s = %v; want a decline of %s", tc.lang, tc.plat, err, tc.name)
			}
		})
	}
}
