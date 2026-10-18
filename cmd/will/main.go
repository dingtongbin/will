// Command will is the will compiler and tool driver.
package main

import (
	"os"

	"github.com/will-lang/will/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
