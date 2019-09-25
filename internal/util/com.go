// ComMessages are sent between go processes over channels to communicate with each other.
// A ComChannel wraps channels so that ComMessages can easily be sent in both
// directions(send and receive).
package util

import (
	"crypto/sha1"

	"github.com/jmatss/torc/internal/torrent"
)

const (
	ChanSize = 10 // arbitrary value
)

type ComId int

const (
	// Sent from "client" down towards "handlers"
	Add ComId = iota
	Delete
	Start // Start stopped/paused torrent download
	Stop  // Stop/pause download
	Quit
)

const (
	// Sent from "handlers" up towards "client", starts at index 20
	Success ComId = iota + 20 // general error
	Failure
	Complete
	NoConnection // "no internet connection"
	DiskFull
	PermissionDenied
)

func (id ComId) String() string {
	if id < 20 {
		return []string{
			"Add",
			"Delete",
			"Start",
			"Stop",
			"Quit",
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
	Error   error            // Set when sending a response. Error == nil: everything fine.
	Torrent *torrent.Torrent // Only used for "Add" messages.

	// InfoHash specifies which torrent(s) this ComMessage is
	// meant for. If nil/empty, assume all torrents.
	InfoHash [][sha1.Size]byte
}

type ComChannel struct {
	Send chan ComMessage
	Recv chan ComMessage
}

func NewComChannel() ComChannel {
	return ComChannel{
		Send: make(chan ComMessage, ChanSize),
		Recv: make(chan ComMessage, ChanSize),
	}
}

// Send a request and receive a response("Respond").
func (cc *ComChannel) Request(id ComId, message string, infoHash ...[sha1.Size]byte) ComMessage {
	cm := ComMessage{
		Id:       id,
		Message:  message,
		InfoHash: infoHash,
	}
	cc.Send <- cm

	return <-cc.Recv
}

func (cc *ComChannel) Respond(id ComId, err error, infoHash ...[sha1.Size]byte) {
	cm := ComMessage{
		Id:       id,
		Error:    err,
		InfoHash: infoHash,
	}
	cc.Send <- cm
}
