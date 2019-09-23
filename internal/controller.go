package internal

import (
	"fmt"

	"github.com/jmatss/torc/internal/torrent"
	. "github.com/jmatss/torc/internal/util" // "oom" util
)

var (
	PeerId string
)

func Controller(peerId string, com ComChannel) {
	PeerId = peerId
	// TODO: fetch torrents from disk and send to handlers when created
	torrents := fetchTorrents()

	// Spawn handlers. Every handler will be in charge of a specific torrent
	// with the InfoHash of the torrent being used as the key in the handlers map.
	handlers := make(map[string]ComChannel, len(torrents))
	for _, tor := range torrents {
		infoHash := string(tor.Tracker.InfoHash[:])

		handlers[infoHash] = NewComChannel()
		go Handler(tor, handlers[infoHash])
	}

	var msg ComMessage
	for {
		msg = <-com.Recv

		switch msg.Id {
		case Add:
			if msg.Torrent == nil {
				com.Respond(msg.Id, msg.InfoHash,
					fmt.Errorf("no torrent specified when trying to \"%s\"", msg.Id.String()),
				)
			}

			_, ok := torrents[string(msg.Torrent.Tracker.InfoHash[:])]
			if ok {
				com.Respond(msg.Id, msg.InfoHash,
					fmt.Errorf("tried to add a torrent that already exists"),
				)
			}

			torrents[string(msg.Torrent.Tracker.InfoHash[:])] = msg.Torrent
			// TODO: CODE add handler in "handlers" && go torrentHandler()

			com.Respond(msg.Id, msg.InfoHash, nil)
		case Delete:
			if msg.InfoHash == nil {
				com.Respond(msg.Id, msg.InfoHash,
					fmt.Errorf("no torrent specified when trying to \"%s\"", msg.Id.String()),
				)
			}

			// Loop through and delete all specified torrents.
			// If a specified item doesn't exist (ex. if it already has been deleted),
			// it will be skipped and NO error will be returned.
			for _, infoHash := range msg.InfoHash {
				_, ok := torrents[string(infoHash[:])]
				if ok {
					// TODO: CODE kill handler for specified torrent.
					//  Might wan't to loop through and kill all other torrents
					//  even if an error is returned from current iteration and
					//  then let the "client" know which torrents that couldn't
					//  be deleted, so it can decide what to do.
				}
			}

			com.Respond(msg.Id, msg.InfoHash, nil)
		case Start, Stop:
			if msg.InfoHash == nil {
				com.Respond(msg.Id, msg.InfoHash,
					fmt.Errorf("no torrent specified when trying to \"%s\"", msg.Id.String()),
				)
			}

			// Loop through and start/stop all specified torrents.
			// If a specified item doesn't exist
			// it will be skipped and NO error will be returned.
			for _, infoHash := range msg.InfoHash {
				_, ok := torrents[string(infoHash[:])]
				if ok {
					// TODO: CODE start/stop
				}
			}

			com.Respond(msg.Id, msg.InfoHash, nil)
		case Quit:
			for key := range handlers {
				resp := handlers[key].Request(Quit, nil, "")
				if resp.Error == nil {
					// TODO: something went wrong, do something(?) or just ignore since
					//  the program is quiting anyways, the handler will be killed either way
				}
			}

			return
		}
	}
}

// TODO: currently only returns empty
func fetchTorrents() map[string]*torrent.Torrent {
	return make(map[string]*torrent.Torrent, 0)
}
