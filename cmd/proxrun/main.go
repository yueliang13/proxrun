package main

import (
	"github.com/yueliang13/proxrun/internal/app"
	"os"
)

func main() { os.Exit(app.New(os.Stdin, os.Stdout, os.Stderr).Run(os.Args[1:])) }
