// Command gws-go provides a focused Google Workspace CLI.
package main

import (
	"fmt"
	"os"

	"github.com/hairizuanbinnoorazman/gws-go/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
