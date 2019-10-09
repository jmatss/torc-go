package main

import (
	"bufio"
	"fmt"
	"github.com/jmatss/torc/internal"
	"log"
	"os"
	"strings"

	"github.com/jmatss/torc/internal/torrent"
	"github.com/jmatss/torc/internal/util/com"
)

func main() {
	controllerId := "controller"
	comController := com.New()
	go internal.Controller(comController, controllerId)

	reader := bufio.NewReader(os.Stdin)
	for {
		input, err := reader.ReadString('\n')
		if err != nil {
			fmt.Fprint(os.Stderr, "incorrect input, try again\n")
			continue
		}
		input = strings.TrimSpace(input)
		cmd := strings.Split(input, " ")

		switch cmd[0] {
		case "q", "quit":
			os.Exit(0)
		case "a", "add":
			if len(cmd) != 2 {
				fmt.Fprint(os.Stderr, "incorrect amount of arguments, expected: %d, got: %d: "+
					"specify torrent filename to add\n", 2, len(cmd))
				continue
			}

			filename := cmd[1]
			tor, err := torrent.NewTorrent(filename)
			if err != nil {
				fmt.Fprint(os.Stderr, "unable to create torrent \"%s\": %v", filename, err)
				continue
			}

			ok := comController.SendChild(com.Add, nil, tor, controllerId)
			if ok {
				log.Println("Add sent")
			} else {
				log.Println("Unable to Add")
			}
		default:
			log.Println("incorrect command, try again")
		}
	}
}
