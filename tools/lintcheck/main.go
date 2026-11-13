// Command lintcheck runs the project's own analyzers as a vet tool:
//
//	go vet -vettool=bin/lintcheck ./...
//
// `make lint` builds and runs it next to golangci-lint.
package main

import (
	"golang.org/x/tools/go/analysis/unitchecker"
)

func main() {
	unitchecker.Main(httpClientTimeout)
}
