package main

import (
	"fmt"
	"os"

	"github.com/jmatss/torc/internal"
)

func main() {
	args := os.Args
	if len(args) != 2 {
		fmt.Fprintf(os.Stderr, "Enter torrent file as argument")
		os.Exit(0)
	}

	internal.Run(args[1])
}
