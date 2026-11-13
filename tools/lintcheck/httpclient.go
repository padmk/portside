package main

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

// httpClientTimeout reports an http.Client literal that does not set
// Timeout. A request through such a client waits forever for a peer that
// accepts the connection and never answers. A client whose requests carry
// their own deadline, or one for a long-lived stream such as a WebSocket,
// says so with an explicit Timeout: 0 and a comment.
var httpClientTimeout = &analysis.Analyzer{
	Name:     "httpclienttimeout",
	Doc:      "reports http.Client literals without a Timeout field",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run: func(pass *analysis.Pass) (any, error) {
		ins := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
		ins.Preorder([]ast.Node{(*ast.CompositeLit)(nil)}, func(n ast.Node) {
			lit := n.(*ast.CompositeLit)
			if !isHTTPClient(pass.TypesInfo.TypeOf(lit)) {
				return
			}
			for _, el := range lit.Elts {
				if kv, ok := el.(*ast.KeyValueExpr); ok {
					if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Timeout" {
						return
					}
				}
			}
			pass.Reportf(lit.Pos(), "http.Client without Timeout: set one, or Timeout: 0 with a comment for requests bounded by their context or a long-lived stream")
		})
		return nil, nil
	},
}

func isHTTPClient(t types.Type) bool {
	named, ok := types.Unalias(t).(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	return obj.Pkg() != nil && obj.Pkg().Path() == "net/http" && obj.Name() == "Client"
}
