package reapi_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/frontseat-dev/frontseat/server/internal/exec/reapi"

	"github.com/frontseat-dev/frontseat/server/internal/exec"
	"github.com/frontseat-dev/frontseat/server/testsupport/testgrid"
)

// Measure what an action the daemon was answered for costs, using the
// real grid's Key implementation.
func BenchmarkAnActionAnsweredFromMemory(b *testing.B) {
	for _, inputs := range []int{4, 40} {
		b.Run(fmt.Sprintf("%d-inputs", inputs), func(b *testing.B) {
			grid, err := dialB(b)
			if err != nil {
				b.Fatal(err)
			}
			files := map[string]string{}
			held := said{}
			var names []string
			for i := range inputs {
				name := fmt.Sprintf("module/pkg%d/file%d.go", i%4, i)
				files[name] = fmt.Sprintf("package p%d\n", i)
				names = append(names, name)
			}
			dir := rootB(b, files)
			for name, content := range files {
				held[filepath.Join(dir, filepath.FromSlash(name))] = content
			}
			action := exec.Action{
				Label:            "//module:go.compile",
				Root:             dir,
				Inputs:           names,
				Files:            held,
				WorkingDirectory: "module",
				Arguments:        []string{"/bin/sh", "-c", "true"},
				Environment:      map[string]string{"A": "1"},
				Platform:         testgrid.Platform(),
			}
			kept := &exec.Remembering{Executor: grid}
			if _, err := kept.Execute(context.Background(), action); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				done, err := kept.Execute(context.Background(), action)
				if err != nil || !done.Cached {
					b.Fatal(err, done.Cached)
				}
			}
		})
	}
}

func dialB(b *testing.B) (*reapi.Grid, error) {
	grid, err := reapi.Dial(context.Background(), reapi.Config{Address: testgrid.Address(b)})
	if err == nil {
		b.Cleanup(func() { grid.Close() })
	}
	return grid, err
}

func rootB(b *testing.B, files map[string]string) string {
	dir := b.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	return dir
}
