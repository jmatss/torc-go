package internal

import (
	"crypto/sha1"
	"fmt"

	"github.com/jmatss/torc/internal/torrent"
)

const (
	ChanSize = 10 // arbitrary value
)

const (
	// Sent from "client" to "controller"
	// Also used to send responses
	Add ComId = iota
	Delete
	Start // Start stopped/paused torrent download
	Stop  // Stop/pause download
)

const (
	// Sent from "controller" to "client", starts at index 20
	// Also used to send responses
	Complete     ComId = iota + 20
	NoConnection       // "no internet connection"
	DiskFull
	PermissionDenied
)

type ComId int

func (id ComId) String() string {
	if id < 20 {
		return []string{
			"Add",
			"Delete",
			"Start",
			"Stop",
		}[id]
	} else {
		return []string{
			"Complete",
			"NoConnection",
			"DiskFull",
			"PermissionDenied",
		}[id%20]
	}
}

type ComMessage struct {
	Id      ComId
	Message string

	// InfoHash specifies which torrent(s) this ComMessage is
	// meant for. If nil/empty, assume all torrents.
	InfoHash [][sha1.Size]byte
	Torrent  *torrent.Torrent // used when a new torrent is added

	// Set when sending a response.
	// Response == nil, everything alright.
	Response error
}

func Controller(torrents map[string]*torrent.Torrent, recv chan ComMessage, send chan ComMessage) {
	handlers := make([]chan ComMessage, len(torrents))
	for i := range handlers {
		handlers[i] = make(chan ComMessage, ChanSize)
		// TODO: go torrentHandler(CHANNEL handlers[i])
	}

	select {
	case req := <-recv:
		switch req.Id {
		case Add:
			if req.Torrent == nil {
				send <- NewComResponse(req.Id, req.InfoHash,
					fmt.Errorf("no torrent specified when trying to \"%s\"",
						req.Id.String()),
				)
			}

			_, ok := torrents[string(req.Torrent.Tracker.InfoHash[:])]
			if !ok {
				send <- NewComResponse(req.Id, req.InfoHash,
					fmt.Errorf("tried to add an torrent that already exists"),
				)
			}

			torrents[string(req.Torrent.Tracker.InfoHash[:])] = req.Torrent
			// TODO: CODE add handler in "handlers" && go torrentHandler()

			send <- NewComResponse(req.Id, req.InfoHash, nil)
		case Delete:
			if req.InfoHash == nil {
				send <- NewComResponse(req.Id, req.InfoHash,
					fmt.Errorf("no torrent specified when trying to \"%s\"",
						req.Id.String()),
				)
			}

			// Loop through and delete all specified torrents.
			// If a specified item doesn't exist (ex. if it already has been deleted),
			// it will be skipped and NO error will be returned.
			for _, infoHash := range req.InfoHash {
				_, ok := torrents[string(infoHash[:])]
				if ok {
					// TODO: CODE kill handler for specified torrent.
					//  Might wan't to loop through and kill all other torrents
					//  even if an error is returned from current iteration and
					//  then let the "client" know which torrents that couldn't
					//  be deleted, so it can decide what to do.
				}
			}

			send <- NewComResponse(req.Id, req.InfoHash, nil)
		case Start, Stop:
			if req.InfoHash == nil {
				send <- NewComResponse(req.Id, req.InfoHash,
					fmt.Errorf("no torrent specified when trying to \"%s\"",
						req.Id.String()),
				)
			}

			// Loop through and start/stop all specified torrents.
			// If a specified item doesn't exist
			// it will be skipped and NO error will be returned.
			for _, infoHash := range req.InfoHash {
				_, ok := torrents[string(infoHash[:])]
				if ok {
					// TODO: CODE start/stop
				}
			}

			send <- NewComResponse(req.Id, req.InfoHash, nil)
		}
	}
}

func NewComRequest(id ComId, infoHash [][sha1.Size]byte, message string) ComMessage {
	return ComMessage{
		Id:       id,
		Message:  message,
		InfoHash: infoHash,
	}
}

func NewComResponse(id ComId, infoHash [][sha1.Size]byte, err error) ComMessage {
	return ComMessage{
		Id:       id,
		Response: err,
		InfoHash: infoHash,
	}
}

func torrentHandler() {

}
