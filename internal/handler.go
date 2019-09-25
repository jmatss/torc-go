package internal

import (
	"time"

	"github.com/jmatss/torc/internal/torrent"
	. "github.com/jmatss/torc/internal/util"
)

// Handler in charge of one specific torrent.
// Will loop forever and wait for either a message it's parent (controller)
// or a timeout at which it should poll the tracker for new peers.
func Handler(tor *torrent.Torrent, com ComChannel, peerId string) {
	err := tor.Request(peerId)
	com.Send(Add, err, nil, tor.Tracker.InfoHash)
	if err != nil {
		return
	}

	err = tor.Request(peerId)
	if err != nil {
		com.Send(Failure, err, nil, tor.Tracker.InfoHash)
		return
	}

	// TODO: CODE start init peers
	interval := tor.Tracker.Interval

	if interval <= 0 {
		//tor.R
		//err := torrent.Request

	}

	// TODO: implement
	for {
		var msg ComMessage
		select {
		case msg = <-com.RecvChan:

		case <-time.After(3 * time.Second):

		}
	}

}
