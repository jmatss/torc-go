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

	// Fetch messages from controller and log them
	go func() {
		for {
			received := <-comController.Parent
			log.Printf("MSG FROM CONTROLLER:\nID: %s\nerr: %s\nchild: %s\n",
				received.Id.String(), received.Error, received.Child)
			switch received.Id {
			case com.List:
				for i, file := range received.Torrent.Info.Files {
					log.Printf("file %d: %s\n", i, strings.Join(file.Path, "/"))
				}
				log.Printf("peers: %d\n", len(received.Torrent.Tracker.Peers))
				for i, peer := range received.Torrent.Tracker.Peers {
					log.Printf("peer %d: %s\n", i, peer.HostAndPort)
				}
				log.Printf("seeders: %d\n", received.Torrent.Tracker.Seeders)
				log.Printf("leechers: %d\n", received.Torrent.Tracker.Leechers)
				log.Printf("downloaded: %d\n", received.Torrent.Tracker.Downloaded)
				log.Printf("uploaded: %d\n", received.Torrent.Tracker.Uploaded)
				log.Printf("bitfield: %v\n\n", received.Torrent.Tracker.BitFieldHave)
			}
		}
	}()

	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Printf("\n> ")
		input, err := reader.ReadString('\n')
		if err != nil {
			fmt.Fprintf(os.Stderr, "incorrect input, try again\n")
			continue
		}
		input = strings.TrimSpace(input)
		cmd := strings.Split(input, " ")

		switch cmd[0] {
		case "q", "quit":
			os.Exit(0)
		case "a", "add":
			if len(cmd) != 2 {
				fmt.Fprintf(os.Stderr, "incorrect amount of arguments, expected: %d, got: %d: "+
					"specify torrent filename to add\n", 2, len(cmd))
				continue
			}

			filename := cmd[1]
			tor, err := torrent.NewTorrent(filename)
			if err != nil {
				fmt.Fprintf(os.Stderr, "unable to create torrent \"%s\": %v", filename, err)
				continue
			}

			ok := comController.SendChild(com.Add, nil, nil, tor, controllerId)
			if !ok {
				log.Println("Unable to Add")
				log.Printf("Count: %d\n", comController.CountChildren())
				log.Printf("Exists: %v\n", comController.Exists(controllerId))
			}
		case "ls":
			comController.SendChildren(com.List, nil)
		default:
			log.Println("incorrect command, try again")
		}
	}
}
