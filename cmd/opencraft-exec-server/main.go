// Command opencraft-exec-server is the opencraft exec-server deployment
// entrypoint (skeleton).
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	listen := flag.String("listen", "", "unix socket path to listen on")
	flag.Parse()
	if *listen == "" {
		fmt.Fprintln(os.Stderr, "exec-server: -listen is required")
		os.Exit(2)
	}
	fmt.Printf("opencraft-exec-server skeleton (listen=%s); protocol types in internal/execserver\n", *listen)
}
