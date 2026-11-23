package source_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/frontseat-dev/frontseat/build/engine/engine/source"
	"github.com/frontseat-dev/frontseat/build/engine/pattern"
	"github.com/frontseat-dev/frontseat/testsupport/testbudget"
)

// BenchmarkTake is a repository of a thousand files, ten in each of a
// hundred directories, listed as a run that begins lists it.
func BenchmarkTake(b *testing.B) {
	root := thousandFiles(b)
	taking(b, root)
}

// thousandFiles is a repository of a thousand files, ten in each of a
// hundred directories.
func thousandFiles(b *testing.B) string {
	b.Helper()
	root := b.TempDir()
	for d := range 100 {
		directory := filepath.Join(root, fmt.Sprintf("module%03d", d))
		if err := os.MkdirAll(directory, 0o755); err != nil {
			b.Fatal(err)
		}
		for f := range 10 {
			if err := os.WriteFile(filepath.Join(directory, fmt.Sprintf("file%d.go", f)), []byte("package module\n"), 0o644); err != nil {
				b.Fatal(err)
			}
		}
	}
	return root
}

// taking lists the repository at root as a run that begins lists it,
// for as long as the benchmark loops.
func taking(b *testing.B, root string) {
	b.Helper()
	tree := source.Tree{Root: root, Paths: map[string]string{"": "."}}
	for b.Loop() {
		if _, err := new(source.Keeper).Take(context.Background(), tree); err != nil {
			b.Fatal(err)
		}
	}
}

// Measured 2026-11-06 on an Apple M-series laptop, three runs of
// `go test -run TestBudgetOfListingARepository -v
// ./internal/engine/source/`: 3.32 to 3.43 ms and 7,462 allocations.
// The time allowed is ten times that, the allocations a tenth more.
func TestBudgetOfListingARepository(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 34 * time.Millisecond, Allocs: 8200}, BenchmarkTake)
}

// BenchmarkTakeIgnoring is the same repository with an ignore file of
// ten patterns, one of which lets a directory back in: what listing
// costs when every directory and file is held to the ignore file.
func BenchmarkTakeIgnoring(b *testing.B) {
	root := thousandFiles(b)
	ignore := "# fixtures\n/module09*/\n!/module099/\nexamples/\ntestdata/\n*.md\n*.log\n/dist/\n/build/\n/target/\n/node_modules/\n"
	if err := os.WriteFile(filepath.Join(root, source.IgnoreFile), []byte(ignore), 0o644); err != nil {
		b.Fatal(err)
	}
	taking(b, root)
}

// Measured 2026-11-08 on an Apple M4 Max, three runs of
// `go test -run '^$' -bench '^BenchmarkTakeIgnoring$' -benchmem -count=3
// ./build/engine/engine/source/`: 3.19 to 3.65 ms and 7,913
// allocations, against 2.78 to 3.02 ms and 7,504 for BenchmarkTake in
// the same runs. The time allowed is ten times that, the allocations a
// tenth more.
func TestBudgetOfListingARepositoryWithAnIgnoreFile(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 36 * time.Millisecond, Allocs: 8700}, BenchmarkTakeIgnoring)
}

// thousand is a repository of a thousand modules of four files each, as
// a run that has begun sees it.
func thousand(b *testing.B) (*source.Snapshot, string) {
	root := b.TempDir()
	for d := range 1000 {
		directory := filepath.Join(root, fmt.Sprintf("m%04d", d))
		if err := os.MkdirAll(directory, 0o755); err != nil {
			b.Fatal(err)
		}
		for _, name := range []string{"go.mod", "main.go", "word.go", "word_test.go"} {
			if err := os.WriteFile(filepath.Join(directory, name), []byte("package main\n"), 0o644); err != nil {
				b.Fatal(err)
			}
		}
	}
	run, err := new(source.Keeper).Take(context.Background(), source.Tree{Root: root, Paths: map[string]string{"": "."}})
	if err != nil {
		b.Fatal(err)
	}
	return run, root
}

// BenchmarkGlob is the files of one module found among a thousand
// modules': what staging one action asks.
func BenchmarkGlob(b *testing.B) {
	run, _ := thousand(b)
	patterns := []string{"m0500/**/*.go", "m0500/go.mod", "m0500/go.sum", "!m0500/**/*_test.go"}
	for b.Loop() {
		found, err := run.Glob(context.Background(), "", patterns)
		if err != nil || len(found) != 3 {
			b.Fatalf("found %v, err %v; want the module's three files", found, err)
		}
	}
}

// Select one module's native files and two independently scoped fixture
// groups from the same thousand-module snapshot, including an overlap.
func BenchmarkScopedSourceGroups(b *testing.B) {
	run, _ := thousand(b)
	groups := []pattern.Group{
		{Directory: "m0500", Patterns: []string{"*.go", "!word_test.go"}},
		{Directory: "m0500", Patterns: []string{"word.go"}},
		{Directory: "m0501", Patterns: []string{"go.mod"}},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		found, err := run.Select(context.Background(), "", groups)
		if err != nil || len(found) != 3 {
			b.Fatalf("found %v, err %v; want three files", found, err)
		}
	}
}

// Measured on 2026-11-15, Apple M4 Max: 779.1–783.8 ns/op,
// 160 B/op and 5 allocs/op over three samples of:
// mise exec -- go test ./build/engine/engine/source -run '^$' -bench '^BenchmarkScopedSourceGroups$' -benchmem -count=3
func TestBudgetOfScopedSourceGroups(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 8 * time.Microsecond, Allocs: 6}, BenchmarkScopedSourceGroups)
}

// BenchmarkFile is a file the run has asked about asked about again:
// what each input of each action after the first to read it costs.
func BenchmarkFile(b *testing.B) {
	run, root := thousand(b)
	file := filepath.Join(root, "m0500", "main.go")
	if _, _, _, err := run.File(file); err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		if _, _, _, err := run.File(file); err != nil {
			b.Fatal(err)
		}
	}
}

// Measured 2026-11-06 on an Apple M-series laptop, three runs of
// `go test -run 'TestBudgetOfFindingAModulesFiles|TestBudgetOfAskingAboutAFileAgain'
// -v ./internal/engine/source/`: finding a module's files took 1.5 to
// 1.7 µs and 13 allocations, asking about a file again 2.6 to 3.8 µs
// and 4. The time allowed is ten times that, the allocations a tenth
// more.
//
// Finding took 420 to 570 µs while every file of the repository was
// held to the patterns, and asking 35 to 64 µs and 13 allocations while
// the file was opened to be looked at.
func TestBudgetOfFindingAModulesFiles(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 17 * time.Microsecond, Allocs: 15}, BenchmarkGlob)
}

func TestBudgetOfAskingAboutAFileAgain(t *testing.T) {
	testbudget.Hold(t, testbudget.Budget{Time: 38 * time.Microsecond, Allocs: 5}, BenchmarkFile)
}
