// Package proxrun contains the defaults embedded in each release binary.
package proxrun

import (
	"embed"
	"strings"
)

//go:embed config/proxy.conf config/rules.yaml lib/shell.sh lib/shell.fish VERSION
var assets embed.FS

func Asset(name string) []byte {
	data, err := assets.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return data
}

func Version() string { return strings.TrimSpace(string(Asset("VERSION"))) }
