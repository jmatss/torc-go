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
func Handler(tor *torrent.Torrent, controllerCom ComChannel, peerId string) {
	retryCount := 0

	// Make tracker request and send back result to the controller over the ComChannel.
	// The handler will kill itself if it isn't able to complete it's tracker request.
	if err := tor.Request(peerId); err != nil {
		controllerCom.Send(TotalFailure, err, nil, tor.Tracker.InfoHash)
		return
	} else {
		controllerCom.Send(Success, nil, nil, tor.Tracker.InfoHash)
	}

	// TODO: Maybe add an extra error message at the end of the loop
	//  if all peers fail.
	for _, peer := range tor.Tracker.Peers {
		conn, err := peer.Handshake(peerId, tor.Tracker.InfoHash)
		if err != nil {
			controllerCom.Send(Failure, err, nil, tor.Tracker.InfoHash)
		} else {
			peer.Connection = conn
		}
	}

	// TODO: implement
	for {
		var msg ComMessage
		interval := time.Duration(tor.Tracker.Interval)

		select {
		case msg = <-controllerCom.RecvChan:

		case <-time.After(interval * time.Second):
			if err := tor.Request(peerId); err != nil {
				retryCount++
				if retryCount >= MaxRetryCount {
					controllerCom.Send(TotalFailure, err, nil, tor.Tracker.InfoHash)
					return
				} else {
					controllerCom.Send(Failure, err, nil, tor.Tracker.InfoHash)
				}
			} else {
				retryCount = 0
			}
		}
	}

}

// TODO: implements some sort of peerHandler.
//  How should the "handler" keep track of all the peers that currently have a peerHandler?
//  How should new peerHandlers be added after a tracker request?
func PeerHandler() {

}
