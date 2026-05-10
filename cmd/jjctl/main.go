package main

import (
	"os"

	"github.com/jungju/jj/internal/jjctl"
)

func main() {
	os.Exit(jjctl.Main(os.Args[1:], os.Stdout, os.Stderr))
}
