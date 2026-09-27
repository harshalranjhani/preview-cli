package main

import (
	"os"

	"github.com/harshalranjhani/preview-cli/internal/cli"
)

// version is overridden by release builds.
var version = "0.1.1"

func main() {
	os.Exit(cli.Execute(version))
}
