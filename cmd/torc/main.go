package main

import (
	"fmt"
	"os"

	"github.com/jmatss/torc/internal/client"
)

func main() {
	args := os.Args
	if len(args) != 2 {
		fmt.Fprintf(os.Stderr, "Enter torr toent file as argument")
		os.Exit(0)
	}

	client.Start(args[1])
}
