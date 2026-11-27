// SPDX-FileCopyrightText: 2026 tao3k team and Contributors
//
// SPDX-License-Identifier: Apache-2.0 AND LGPL-2.1-or-later

package async

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestStructuredConcurrencyASTGuard mechanically enforces the Quint
// StructuredCancellation.qnt (child-quiescence-barrier) invariant across
// marlin-mygo's frontend runtime and marketplace packages: production code
// outside runtime/async itself must NEVER spawn raw fire-and-forget goroutines
// (`go ...`); all background work must execute inside async.StructuredScope or
// async.FencedLane so teardown drains deterministically.
func TestStructuredConcurrencyASTGuard(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	fset := token.NewFileSet()
	guardedDirs := []string{
		"runtime/kit",
		"runtime/style",
		"runtime/widgets",
		"runtime/mygoshell",
		"control/marketplace",
	}
	checked := 0
	for _, sub := range guardedDirs {
		root := filepath.Join(repoRoot, sub)
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, _ := filepath.Rel(repoRoot, path)
			file, parseErr := parser.ParseFile(fset, path, nil, 0)
			if parseErr != nil {
				t.Errorf("parse %s: %v", rel, parseErr)
				return nil
			}
			checked++
			ast.Inspect(file, func(n ast.Node) bool {
				if gs, ok := n.(*ast.GoStmt); ok {
					pos := fset.Position(gs.Pos())
					t.Errorf("Quint StructuredCancellation.qnt violation in %s:%d: raw `go` statement forbidden outside runtime/async (use async.StructuredScope.Go or async.FencedLane)", rel, pos.Line)
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", sub, err)
		}
	}
	if checked == 0 {
		t.Fatalf("expected to inspect marlin-mygo Go files, found 0")
	}
}
