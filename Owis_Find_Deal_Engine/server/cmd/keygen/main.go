// Command keygen creates an API key for a client application.
//
//	go run ./cmd/keygen mobile
//
// Give the key to the client app; put the name:hash entry in API_KEYS.
package main

import (
	"fmt"
	"os"

	"owis_find_deal_engine/internal/auth"
)

func main() {
	if len(os.Args) != 2 || os.Args[1] == "" {
		fmt.Fprintln(os.Stderr, "usage: keygen <client-name>")
		os.Exit(2)
	}
	key, hash, err := auth.GenerateKey()
	if err != nil {
		fmt.Fprintln(os.Stderr, "keygen:", err)
		os.Exit(1)
	}
	fmt.Printf("API key (give to the client, shown once): %s\n", key)
	fmt.Printf("API_KEYS entry (server config):          %s:%s\n", os.Args[1], hash)
}
