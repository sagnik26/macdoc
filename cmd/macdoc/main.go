// Command macdoc is a safe, local-first storage and performance doctor for
// developer Macs.
package main

import (
	"os"

	"github.com/sagnik26/macdoc/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
