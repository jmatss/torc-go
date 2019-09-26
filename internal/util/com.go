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
	Failure ComId = iota + 20 // general error
	Complete
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
	Error   error            // Set when sending a response. Error == nil: everything fine.
	Torrent *torrent.Torrent // Only used for "Add" messages.

	// InfoHash specifies which torrent(s) this ComMessage is
	// meant for. If nil/empty, assume all torrents.
	InfoHash [][sha1.Size]byte
}

type ComChannel struct {
	SendChan chan ComMessage
	RecvChan chan ComMessage
}

func NewComChannel() ComChannel {
	return ComChannel{
		SendChan: make(chan ComMessage, ChanSize),
		RecvChan: make(chan ComMessage, ChanSize),
	}
}

func (cc *ComChannel) Recv() ComMessage {
	return <-cc.RecvChan
}

// Send a new message on the ComChannel.
func (cc *ComChannel) Send(
	id ComId,
	error error,
	torrent *torrent.Torrent,
	infoHash ...[sha1.Size]byte,
) {
	cm := ComMessage{id, error, torrent, infoHash}
	cc.SendChan <- cm
}

// Send a new message on the ComChannel and block and wait on a response.
func (cc *ComChannel) SendAndRecv(
	id ComId,
	error error,
	torrent *torrent.Torrent,
	infoHash ...[sha1.Size]byte,
) ComMessage {
	cc.Send(id, error, torrent, infoHash...)
	return <-cc.RecvChan
}

// Send a copy of a message on the ComChannel.
func (cc *ComChannel) SendCopy(msg ComMessage) {
	cc.SendChan <- msg
}

func (cc *ComChannel) SendCopyAndRecv(msg ComMessage) ComMessage {
	cc.SendCopy(msg)
	return <-cc.RecvChan
}

// Change the error message of a copy and send it on the ComChannel.
func (cc *ComChannel) SendCopyError(msg ComMessage, err error) {
	msg.Error = err
	cc.SendChan <- msg
}
