package internal

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/jmatss/torc/internal/torrent"
	. "github.com/jmatss/torc/internal/util" // "com"
)

func Run() {
	com := NewComChannel()
	go Controller(com)

	reader := bufio.NewReader(os.Stdin)
	var received ComMessage
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

			filename := filepath.FromSlash(cmd[1])
			tor, err := torrent.NewTorrent(filename)
			if err != nil {
				fmt.Fprint(os.Stderr, "unable to create torrent \"%s\": %v", filename, err)
				continue
			}

			received = com.SendAndRecv(Add, nil, tor, tor.Tracker.InfoHash)
			log.Printf("RESULT: %v", received)
		default:
			log.Println("incorrect command, try again")
		}
	}
}
