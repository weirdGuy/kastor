package main

import (
	"fmt"
	"os"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		// An empty message means the command already printed everything the
		// user needs (kastor doctor's findings are its output); only the
		// exit status is left to carry.
		if msg := err.Error(); msg != "" {
			fmt.Fprintf(os.Stderr, "kastor: %s\n", msg)
		}
		os.Exit(exitCode(err))
	}
}
