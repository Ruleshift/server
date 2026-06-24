package architecture

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestProductionCoreDoesNotImportGameImplementations(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	paths := []string{"cmd/gateway", "internal/module", "internal/roomcore", "internal/gatewayv2", "internal/controlplane"}
	for _, relative := range paths {
		err := filepath.WalkDir(filepath.Join(root, filepath.FromSlash(relative)), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, spec := range parsed.Imports {
				value, _ := strconv.Unquote(spec.Path.Value)
				if strings.Contains(value, "/internal/game") || strings.Contains(value, "/examples/modules") {
					t.Errorf("production file %s imports module implementation %s", path, value)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestLegacyPackagesAreAbsent(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	for _, relative := range []string{"internal/game", "internal/room", "internal/gateway", "internal/protocol/legacy", "internal/protocol/generated/go/ruleshiftv1"} {
		err := filepath.WalkDir(filepath.Join(root, filepath.FromSlash(relative)), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !entry.IsDir() {
				t.Errorf("legacy file still exists: %s", path)
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
}
