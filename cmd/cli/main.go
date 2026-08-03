package main

import (
	"fmt"
	"os"
)

// CLI entrypoint — Phase 0 stub.
// Streaming REPL lands in Phase 1 (see docs/design.md).
func main() {
	fmt.Fprintln(os.Stderr, "code-agent CLI: scaffold only — implement Phase 1 (docs/design.md)")
	fmt.Fprintln(os.Stderr, "usage (planned): code-agent --base http://127.0.0.1:8080 chat")
	os.Exit(0)
}
