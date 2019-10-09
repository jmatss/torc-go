package internal

import (
	"fmt"
	"time"

	"github.com/jmatss/torc/internal/torrent"
	"github.com/jmatss/torc/internal/util/com"
)

const (
	// Max retries before it gives up on the tracker
	// Will wait tracker.Interval seconds between retries.
	MaxRetryCount = 5
)

// Handler in charge of one specific torrent.
// TODO: setup so that other peers can connect to this handler
func TorrentHandler(comController com.Channel, tor *torrent.Torrent) {
	childId := string(tor.Tracker.InfoHash[:])

	// Make tracker request. This handler will kill itself if it isn't able to
	// complete the tracker request.
	if err := tor.Request(PeerId); err != nil {
		comController.SendParent(com.Add, err, tor, childId)
		return
	}
	comController.SendParent(com.Add, nil, tor, childId)
	comController.AddChild(childId)
	defer comController.RemoveChild(childId)

	// Start up peerHandlers. Every peer handler will be in charge of one peer
	// of this torrent.
	comPeerHandler := com.New()
	// TODO: Maybe add an extra error message at the end of the loop
	//  if all peers fail.
	for _, peer := range tor.Tracker.Peers {
		go PeerHandler(comPeerHandler, &peer, tor)
	}

	retryCount := 0
	intervalTimer := time.NewTimer(time.Duration(tor.Tracker.Interval))
	for {
		select {
		case received := <-comController.GetChildChannel(childId):
			/*
				Received message from "controller"/parent.
			*/
			switch received.Id {
			case com.Remove:
				// TODO: remove files from disk
				comController.SendParent(received.Id, nil, nil, childId)
				return

			case com.Start:
				count := comPeerHandler.CountChildren()
				if count > 0 {
					comController.SendParent(
						received.Id,
						fmt.Errorf("cant start since it isn't stopped, %d go processes "+
							"still running", count),
						nil,
						childId,
					)
				} else {
					for _, peer := range tor.Tracker.Peers {
						go PeerHandler(comPeerHandler, &peer, tor)
					}
					comController.SendParent(received.Id, nil, nil, childId)
				}

			case com.Stop:
				comController.SendChildren(com.Quit)

			case com.Quit:
				return
			}

		case received := <-comPeerHandler.Parent:
			/*
				Received message from one of the "peerHandlers"/children.
			*/
			switch received.Id {
			case com.Success:
				// The peerHandler has executed the commands sent from the controller.
				// Just pass along to the controller so it can see the results.
				comController.SendParentCopy(received, childId)

			case com.Failure, com.TotalFailure:
				// TODO: log
			default:
				// TODO: log
			}

		case <-intervalTimer.C:
			/*
				Interval time expired. Send new tracker request to get updated information.
			*/
			if err := tor.Request(PeerId); err != nil {
				retryCount++
				if retryCount >= MaxRetryCount {
					comController.SendParent(com.TotalFailure, err, nil, childId)
					return
				} else {
					comController.SendParent(com.Failure, err, nil, childId)
				}
			} else {
				retryCount = 0
			}

			// Reset timer
			intervalTimer = time.NewTimer(time.Duration(tor.Tracker.Interval))
		}
	}

}
