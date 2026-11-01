package memory_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestOnlyMemoryImportsMemstore is the seam, mechanized.
//
// The claim protocol is correct only while every racer builds the same key, and
// key construction lives in this package alone. A second importer of memstore is
// a second place to build a key, a second place to read a scope, and eventually
// two authorities again — which is the defect the rebuild exists to remove. So the
// import direction is a test, not a convention.
func TestOnlyMemoryImportsMemstore(t *testing.T) {
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file")
	}
	root := filepath.Join(filepath.Dir(here), "..", "..")
	self := filepath.Join(root, "internal", "memory")

	var offenders []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// vendor is the dependency's own source, not fs's.
			if info.Name() == "vendor" || info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if filepath.Dir(path) == self {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(src), `"github.com/IanLuo/memstore"`) {
			rel, _ := filepath.Rel(root, path)
			offenders = append(offenders, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(offenders) > 0 {
		t.Fatalf("memstore is imported outside internal/memory: %v", offenders)
	}
}

// TestNoPathNamesTheLegacyStore is the other half of the delete list: the legacy
// event database is not read, not imported, not archived and not named by any code
// path. Its absence is structural, so it is checked structurally.
func TestNoPathNamesTheLegacyStore(t *testing.T) {
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file")
	}
	root := filepath.Join(filepath.Dir(here), "..", "..")

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() && (info.Name() == "vendor" || info.Name() == ".git") {
			return filepath.SkipDir
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		if path == here {
			return nil // this file holds the two literals in order to search for them
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, legacy := range []string{".fs/store.db", ".fs/registry.db"} {
			if strings.Contains(string(src), legacy) {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s names the legacy store %s; fs owns no store and migrates nothing", rel, legacy)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}
