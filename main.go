// Command gws-go provides a focused Google Workspace CLI.
package main

import (
	"os"

	"github.com/hairizuanbinnoorazman/gws-go/cmd"
)

func main() {
	os.Exit(cmd.Run(os.Args[1:], os.Stdout, os.Stderr))
}
