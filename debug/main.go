package main

import (
	caddycmd "github.com/caddyserver/caddy/v2/cmd"
	"os"

	// plug in Caddy modules here
	_ "github.com/caddyserver/caddy/v2/modules/standard"
	_ "github.com/lion7/caddytftp"
)

func main() {
	os.Args = []string{"caddy", "run", "--config", "caddy.json"}
	caddycmd.Main()
}
