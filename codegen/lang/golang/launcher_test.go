package golang

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"git.duckfam.us/jonathan/sngl/internal/testrpc"
)

func TestLaunchTest_endToEnd(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "main.go"), `package main

import "git.duckfam.us/jonathan/sngl/pkg/go/testagent"

func init() {
	testagent.RegisterTest("ok", func(t *testagent.T) { t.Log("ran") })
}

func main() { testagent.Main() }
`)
	tr := &Translator{}
	ch, cleanup, err := tr.LaunchTest(context.Background(), dir, tr, nil)
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	defer cleanup()

	w := testrpc.NewWriter(ch)
	r := testrpc.NewReader(ch)

	id, err := w.Request("list", map[string]any{})
	if err != nil {
		t.Fatalf("request list: %v", err)
	}
	resp := readUntilID(t, r, id)
	var listRes struct{ Tests []string }
	_ = json.Unmarshal(resp.Result, &listRes)
	if len(listRes.Tests) != 1 || listRes.Tests[0] != "ok" {
		t.Errorf("list tests = %v", listRes.Tests)
	}

	id, _ = w.Request("run", map[string]any{})
	var sawTestEnd bool
	for {
		m, err := r.Read()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if m.IsResponse() && m.ID != nil && *m.ID == id {
			break
		}
		if m.Method == "testEnd" {
			var args struct{ Status string }
			_ = json.Unmarshal(m.Params, &args)
			if args.Status == "pass" {
				sawTestEnd = true
			}
		}
	}
	if !sawTestEnd {
		t.Errorf("did not observe passing testEnd")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readUntilID(t *testing.T, r *testrpc.Reader, id uint64) *testrpc.Message {
	t.Helper()
	for {
		m, err := r.Read()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if m.IsResponse() && m.ID != nil && *m.ID == id {
			return m
		}
	}
}
