package main

import (
	"fmt"
	"os"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "adl: %v\n", err)
		os.Exit(exitCode(err))
	}
}
