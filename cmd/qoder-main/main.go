package main

import (
	"fmt"
	"os"

	"cpa-qoder/internal/core"
)

func main() {
	if len(os.Args) == 3 && os.Args[1] == "--mcp-schema" {
		if err := core.ServeMCP(os.Args[2], os.Stdin, os.Stdout); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, "CPA-Qoder tool schema helper failed")
			os.Exit(1)
		}
		return
	}
	_, _ = fmt.Fprintln(os.Stderr, "CPA-Qoder is a native CPA plugin; this executable only serves --mcp-schema")
}
