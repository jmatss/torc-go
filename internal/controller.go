// TODO: if receiving error messages from "handler", remove handler from "handlers" map
//  since it will have shut itself down

package internal

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/jmatss/torc/internal/torrent"
	. "github.com/jmatss/torc/internal/util" // "com" util
)

var (
	PeerId string
)

func Controller(clientCom ComChannel) {
	PeerId = newPeerId()
	var received ComMessage

	// Spawn handlers. Every handler will be in charge of a specific torrent
	// with the InfoHash of the torrent being used as the key in the "handlers" map.
	// The "ComChannel" in the map will be used to communicate with the handlers.
	handlers := make(map[string]ComChannel)
	for _, tor := range fetchTorrentsFromDisk() {
		infoHash := string(tor.Tracker.InfoHash[:])
		handlers[infoHash] = createTorrentHandler(tor)

		// TODO: Do this in another loop after this loop to start all handlers
		//  before blocking and receiving the "results"
		// Receive message from newly spawned handler and send it through to
		// the "client" . The message will contain id "Success" or "Failure"
		// depending on how the "start up" went fro the handler.
		clientCom.SendCopy(handlers[infoHash].Recv())
	}

	for {
		received = clientCom.Recv()

		switch received.Id {
		case Add:
			if received.Torrent == nil {
				clientCom.SendCopyError(
					received,
					fmt.Errorf("no torrent specified when trying to \"%s\"",
						received.Id.String()),
				)
			}

			_, ok := handlers[string(received.Torrent.Tracker.InfoHash[:])]
			if ok {
				clientCom.SendCopyError(
					received,
					fmt.Errorf("tried to add a torrent that already exists"),
				)
			}

			infoHash := string(received.Torrent.Tracker.InfoHash[:])

			handlers[infoHash] = createTorrentHandler(received.Torrent)
			// Receive response from newly added handler and sent i through to
			// the "client."
			clientCom.SendCopy(handlers[infoHash].Recv())

		case Delete:
			if received.InfoHash == nil {
				clientCom.SendCopyError(
					received,
					fmt.Errorf("no torrent specified when trying to \"%s\"",
						received.Id.String()),
				)
			}

			// Loop through and delete all specified torrents.
			// If a specified item doesn't exist (ex. if it already has been deleted),
			// it will be skipped and NO error will be returned.
			for _, infoHash := range received.InfoHash {
				if handlerCom, ok := handlers[string(infoHash[:])]; ok {
					clientCom.SendCopy(handlerCom.SendAndRecv(Delete, nil, nil, infoHash))
				}
			}

		case Start, Stop:
			if received.InfoHash == nil {
				clientCom.SendCopyError(
					received,
					fmt.Errorf("no torrent specified when trying to \"%s\"",
						received.Id.String()),
				)
			}

			// Loop through and start/stop all specified torrents.
			// If a specified item doesn't exist,
			// it will be skipped and NO error will be returned.
			for _, infoHash := range received.InfoHash {
				_, ok := handlers[string(infoHash[:])]
				if ok {
					// TODO: CODE start/stop
				}
			}

			clientCom.SendCopy(received)
		case Quit:
			for key := range handlers {
				resp := handlers[key].SendCopyAndRecv(received)
				if resp.Error == nil {
					// TODO: something went wrong, do something(?) or just ignore since
					//  the program is quiting anyways, the handler will be killed either way
				}
			}

			return
		}
	}
}

func newPeerId() string {
	// Generate peer id. Format: -<client id(2 bytes)><version(4 bytes)>-<12 random ascii numbers>
	// use: client id 'UT'(µTorrent) and random version for anonymity
	peerId := make([]byte, 20)
	peerId[0], peerId[1], peerId[2], peerId[7] = '-', 'U', 'T', '-'

	rand.Seed(time.Now().UTC().UnixNano())
	for i := 3; i < len(peerId); i++ {
		if i == 7 {
			continue
		}
		// (ascii 48 -> 57) => (0 -> 9)
		peerId[i] = byte(rand.Intn(10) + 48)
	}

	return string(peerId)
}

// TODO: currently only returns empty
func fetchTorrentsFromDisk() map[string]*torrent.Torrent {
	return make(map[string]*torrent.Torrent, 0)
}

func createTorrentHandler(tor *torrent.Torrent) ComChannel {
	com := NewComChannel()
	go Handler(tor, com, PeerId)
	return com
}
