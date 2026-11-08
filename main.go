// Command zk-chef maintains an LLM-written Zettelkasten vault.
package main

import (
	"os"

	"github.com/smb374/zk-chef/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Environ()))
}
