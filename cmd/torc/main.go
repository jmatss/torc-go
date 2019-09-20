package main

import (
	"log"
	"os"

	"github.com/jmatss/torc/internal/client"
)

func main() {
	args := os.Args
	if len(args) != 2 {
		log.Printf("Enter torrent file as argument")
		os.Exit(0)
	}

	client.Start(args[1])
}
