// Command logdiet ranks log statements by volume and bytes and estimates
// the savings from demoting them below your production log level.
package main

import (
	"os"

	"github.com/JaydenCJ/logdiet/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
