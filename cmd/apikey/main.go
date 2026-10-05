// Command apikey generates an API key for the API, and the API_KEYS entry
// that goes with it:
//
//	go run ./cmd/apikey <name> <read|write>
//
// The key is printed once: hand it to whoever calls the API, and keep only
// the entry, which holds the key's hash, in the API's configuration.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/ajlatour/golang-vibe-starter/internal/apikey"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "apikey:", err)
		os.Exit(2)
	}
}

func run(args []string, out io.Writer) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: apikey <name> <read|write>")
	}
	var scope apikey.Scope
	switch args[1] {
	case "read":
		scope = apikey.Read
	case "write":
		scope = apikey.Write
	default:
		return fmt.Errorf("scope %q: want read or write", args[1])
	}
	key, entry, err := apikey.Generate(args[0], scope)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, `The key, for whoever calls the API. It is shown only now; keep it secret:

  %s

The entry for API_KEYS, which holds only the key's hash (add it, comma-separated, to the others):

  %s
`, key, entry)
	return nil
}
