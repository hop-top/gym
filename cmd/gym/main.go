package main

import (
	"os"

	"hop.top/gym/internal/cli"
)

func main() {
	app := cli.NewApp(os.Stdout, os.Stderr, "")
	os.Exit(app.Run(os.Args[1:]))
}
