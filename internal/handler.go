package internal

import (
	"time"

	"github.com/jmatss/torc/internal/torrent"
	. "github.com/jmatss/torc/internal/util"
)

const (
	MaxRetryCount = 5
)

// Handler in charge of one specific torrent.
// Will loop forever and wait for either a message from it's parent (controller)
// , a timeout at which it should poll the tracker for new peers or an error from
// a remote peer.
// TODO: setup so that other peers can connect to this handler
func Handler(tor *torrent.Torrent, com ComChannel, peerId string) {
	retryCount := 0

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

	// TODO: implement
	for {
		var msg ComMessage

		select {
		case msg = <-com.RecvChan:

		case <-time.After(time.Duration(interval) * time.Second):
			// TODO: implement some sort of retry functionality so that
			//  it just doesn't kill itself after
			err := tor.Request(peerId)
			if err != nil {
				retryCount++
				if retryCount >= MaxRetryCount {
					com.Send(Failure, err, nil, tor.Tracker.InfoHash)
					if err != nil {
						return
					}
				}
			}
		}
	}

}
