// Command staticserver is a trivial static file server used only by
// .claude/launch.json for local browser-preview testing (e.g. the
// mcp-app-host-harness example), so testing doesn't depend on python3's
// http.server module (and the OS firewall prompt that can come with it).
package main

import (
	"log"
	"net/http"
	"os"
)

func main() {
	dir := "."
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	addr := ":8092"
	if len(os.Args) > 2 {
		addr = os.Args[2]
	}
	log.Printf("staticserver: serving %s on %s", dir, addr)
	log.Fatal(http.ListenAndServe(addr, http.FileServer(http.Dir(dir))))
}
