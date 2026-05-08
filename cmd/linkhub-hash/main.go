// Command linkhub-hash reads a password from stdin and emits a bcrypt
// hash on stdout. Exists so the install script (running on the
// Proxmox host) can shell into the LXC and generate the hash from the
// linkhub-hash binary that ships in the same release tarball.
//
// Usage (inside the container):
//
//	echo -n "password" | linkhub-hash > /tmp/lh-hash
//
// Cost factor 12: the modern default; ~250 ms on a typical CPU. This
// runs once per install, so the cost is invisible.
//
// The output is the raw bcrypt string with a single trailing newline.
// The install script captures it via `$(...)` which strips that
// newline cleanly.
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"golang.org/x/crypto/bcrypt"
)

const cost = 12

// bcrypt itself silently truncates inputs longer than 72 bytes. We
// refuse rather than mislead — better to fail at install time than
// to leave the operator with a password that's quietly shorter than
// they think.
const bcryptMaxBytes = 72

func main() {
	pw, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "linkhub-hash: read stdin: %v\n", err)
		os.Exit(1)
	}
	// Trim a single trailing newline if present. Some shells will add
	// one (e.g. `read VAR; echo "$VAR" | linkhub-hash`); we strip it
	// so the password isn't silently extended by "\n". We do NOT
	// TrimSpace — leading/trailing spaces can be legitimate.
	pw = bytes.TrimSuffix(pw, []byte("\n"))
	pw = bytes.TrimSuffix(pw, []byte("\r"))

	if len(pw) == 0 {
		fmt.Fprintln(os.Stderr, "linkhub-hash: empty password on stdin")
		os.Exit(2)
	}
	if len(pw) > bcryptMaxBytes {
		fmt.Fprintf(os.Stderr,
			"linkhub-hash: password is %d bytes; bcrypt's hard limit is %d\n",
			len(pw), bcryptMaxBytes)
		os.Exit(3)
	}

	hash, err := bcrypt.GenerateFromPassword(pw, cost)
	if err != nil {
		fmt.Fprintf(os.Stderr, "linkhub-hash: %v\n", err)
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(hash); err != nil {
		fmt.Fprintf(os.Stderr, "linkhub-hash: write stdout: %v\n", err)
		os.Exit(1)
	}
	// Trailing newline so terminal output looks normal; `$(...)` will
	// strip it before the value is assigned.
	fmt.Println()
}
