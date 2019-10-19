// Contains information related to the bittorrent protocol.
package bittorrent

const (
	// The KeepAlive message doesn't have an id,
	//  set to -1 so that it still can be distinguished.
	KeepAlive MessageId = iota - 1
	Choke
	UnChoke
	Interested
	NotInterested
	Have
	Bitfield
	Request
	Piece
	Cancel
)

// Variables used in the handshake message(s).
var (
	PStr     = []byte("BitTorrent protocol")
	Reserved = []byte{0, 0, 0, 0, 0, 0, 0, 0}
)

type MessageId int

func (id MessageId) String() string {
	// enum indexing starts at "-1", need to increment with 1.
	return []string{
		"KeepAlive",
		"Choke",
		"UnChoke",
		"Interested",
		"NotInterested",
		"Have",
		"Bitfield",
		"Request",
		"Piece",
		"Cancel",
	}[id+1]
}
