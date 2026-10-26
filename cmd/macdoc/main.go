// Command macdoc is a safe, local-first storage and performance doctor for
// developer Macs.
package main

import (
	"os"

	"macdoc/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
