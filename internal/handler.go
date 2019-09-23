package internal

import (
	"time"

	"github.com/jmatss/torc/internal/torrent"
	. "github.com/jmatss/torc/internal/util"
)

// Handler in charge of one specific torrent.
// Will loop forever and wait for either a message it's parent (controller)
// or a timeout at which it should poll the tracker for new peers.
func Handler(torrent *torrent.Torrent, com ComChannel) {
	// TODO: CODE start init peers
	interval := torrent.Tracker.Interval

	if interval <= 0 {
		//err := torrent.Request

	}

	// TODO: implement
	for {
		var msg ComMessage
		select {
		case msg = <-com.Recv:

		case <-time.After(3 * time.Second):

		}
	}

}
