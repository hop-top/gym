package main

import (
	"os"

	"hop/skills/internal/cli"
)

func main() {
	app := cli.NewApp(os.Stdout, os.Stderr, "")
	os.Exit(app.Run(os.Args[1:]))
}
