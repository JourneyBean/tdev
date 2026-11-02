package main

import (
	"os"
	"tdev/internal/cli"
)

func main() { os.Exit(cli.Execute(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }
