package main

import (
	"fmt"
	"github.com/jmatss/torc/internal"
	"os"
)

func main() {
	args := os.Args
	if len(args) != 2 {
		fmt.Fprintf(os.Stderr, "Enter torrent file as argument")
		os.Exit(0)
	}

	internal.Run(args[1])
}
