package main

import (
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

// phaseAssign reports a change of a supervised server's phase (proc.ph in
// internal/supervisor) outside transition.go, where transitionLocked is the
// one place that moves a server from phase to phase. An assignment, a ph
// key in a proc literal and taking the field's address all count. Tests
// may build a state by hand.
var phaseAssign = &analysis.Analyzer{
	Name:     "phaseassign",
	Doc:      "reports assignments of the supervisor's proc.ph outside transition.go",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run: func(pass *analysis.Pass) (any, error) {
		if !strings.HasSuffix(pass.Pkg.Path(), "/internal/supervisor") {
			return nil, nil
		}
		allowed := func(pos token.Pos) bool {
			name := filepath.Base(pass.Fset.File(pos).Name())
			return name == "transition.go" || strings.HasSuffix(name, "_test.go")
		}
		isPhase := func(e ast.Expr) bool {
			sel, ok := ast.Unparen(e).(*ast.SelectorExpr)
			return ok && sel.Sel.Name == "ph" && isProc(pass.TypesInfo.TypeOf(sel.X))
		}
		const msg = "the phase changes only in transition.go, through transitionLocked"
		ins := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
		ins.Preorder([]ast.Node{(*ast.AssignStmt)(nil), (*ast.CompositeLit)(nil), (*ast.UnaryExpr)(nil)}, func(n ast.Node) {
			if allowed(n.Pos()) {
				return
			}
			switch n := n.(type) {
			case *ast.AssignStmt:
				for _, lhs := range n.Lhs {
					if isPhase(lhs) {
						pass.Reportf(lhs.Pos(), "%s", msg)
					}
				}
			case *ast.UnaryExpr:
				if n.Op == token.AND && isPhase(n.X) {
					pass.Reportf(n.Pos(), "%s", msg)
				}
			case *ast.CompositeLit:
				if !isProc(pass.TypesInfo.TypeOf(n)) {
					return
				}
				for _, el := range n.Elts {
					if kv, ok := el.(*ast.KeyValueExpr); ok {
						if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "ph" {
							pass.Reportf(kv.Pos(), "%s", msg)
						}
					}
				}
			}
		})
		return nil, nil
	},
}

// isProc reports whether t is the supervisor's proc or a pointer to it.
func isProc(t types.Type) bool {
	if p, ok := types.Unalias(t).(*types.Pointer); ok {
		t = p.Elem()
	}
	named, ok := types.Unalias(t).(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	return obj.Name() == "proc" && obj.Pkg() != nil && strings.HasSuffix(obj.Pkg().Path(), "/internal/supervisor")
}
