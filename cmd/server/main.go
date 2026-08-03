package main

import (
	"fmt"
	"os"
)

// Server entrypoint — Phase 0 stub.
// Full bootstrap lands in Phase 1 (see docs/design.md).
func main() {
	fmt.Fprintln(os.Stderr, "code-agent server: scaffold only — implement Phase 1 (docs/design.md)")
	fmt.Fprintln(os.Stderr, "config example: configs/config.example.yaml")
	os.Exit(0)
}
