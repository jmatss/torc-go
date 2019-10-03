package internal

import (
	"time"

	"github.com/jmatss/torc/internal/torrent"
	. "github.com/jmatss/torc/internal/util"
)

// Handler in charge of one specific torrent.
// Will loop forever and wait for either a message from it's parent (controller)
// , a timeout at which it should poll the tracker for new peers or an error from
// a remote peer.
func Handler(tor *torrent.Torrent, com ComChannel, peerId string) {
	// Make tracker request and send back error to the controller over the ComChannel.
	// The handler will kill itself if it isn't able to complete it's tracker request.
	err := tor.Request(peerId)
	com.Send(Add, err, nil, tor.Tracker.InfoHash)
	if err != nil {
		return
	}

	// TODO: Maybe add an extra error message at the end of the loop
	//  if all peers fail.
	for _, peer := range tor.Tracker.Peers {
		conn, err := peer.Handshake(peerId, tor.Tracker.InfoHash)
		if err != nil {
			com.Send(Failure, err, nil, tor.Tracker.InfoHash)
		} else {
			peer.Connection = conn
		}
	}

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
