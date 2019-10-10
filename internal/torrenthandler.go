package internal

import (
	"fmt"
	"log"
	"time"

	"github.com/jmatss/torc/internal/torrent"
	"github.com/jmatss/torc/internal/util/com"
	"github.com/jmatss/torc/internal/util/cons"
)

const (
	// Max retries before it gives up on the tracker
	// Will wait tracker.Interval seconds between retries.
	MaxRetryCount = 5
	MaxPeers      = 8
)

// Handler in charge of one specific torrent.
// TODO: setup so that other peers can connect to this handler
func TorrentHandler(comController com.Channel, tor *torrent.Torrent) {
	childId := string(tor.Tracker.InfoHash[:])

	log.Printf("TorrentHandler started")

	// Make tracker request. This handler will kill itself if it isn't able to
	// complete the tracker request.
	if err := tor.Request(cons.PeerId); err != nil {
		comController.SendParent(com.Add, nil, err, tor, childId)
		return
	}
	comController.SendParent(com.Add, nil, nil, tor, childId)
	comController.AddChild(childId)
	defer comController.RemoveChild(childId)

	log.Printf("TorrentHandler tracker request done successfully")

	// Start up peerHandlers. Every peer handler will be in charge of one peer
	// of this torrent.
	comPeerHandler := com.New()
	for i := 0; i < len(tor.Tracker.Peers); i++ {
		if i >= MaxPeers {
			break
		}
		go PeerHandler(comPeerHandler, &tor.Tracker.Peers[i], tor)
	}

	retryCount := 0
	intervalTimer := time.NewTimer(time.Duration(tor.Tracker.Interval) * time.Second)
	for {
		select {
		case received := <-comController.GetChildChannel(childId):
			/*
				Received message from "controller"/parent.
			*/
			switch received.Id {
			case com.Remove:
				// TODO: remove files from disk
				comController.SendParent(received.Id, nil, nil, nil, childId)
				return

			case com.Start:
				count := comPeerHandler.CountChildren()
				if count > 0 {
					comController.SendParent(
						received.Id,
						nil,
						fmt.Errorf("cant start since it isn't stopped, %d go processes "+
							"still running", count),
						nil,
						childId,
					)
				} else {
					for _, peer := range tor.Tracker.Peers {
						go PeerHandler(comPeerHandler, &peer, tor)
					}
					comController.SendParent(received.Id, nil, nil, nil, childId)
				}

			case com.Stop:
				comPeerHandler.SendChildren(com.Quit, nil)

			case com.List:
				comController.SendParent(com.List, nil, nil, tor, childId)

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

			case com.Have:
				comPeerHandler.SendChildren(com.Have, received.Data)

			case com.TotalFailure:
				// The peerHandler just died, try and add a new peer (might be the same peer)
				// TODO: select peer in another way, this will always restart the same peer
				//  either some sort of saved order or random
				for i, peer := range tor.Tracker.Peers {
					if !comPeerHandler.Exists(peer.HostAndPort) {
						go PeerHandler(comPeerHandler, &tor.Tracker.Peers[i], tor)
						break
					}
				}
			case com.Failure:
			// TODO: log
			default:
				// TODO: log
			}

		case <-intervalTimer.C:
			log.Printf("TorrentHandler interval timeout")
			/*
				Interval time expired. Send new tracker request to get updated information.
			*/
			if err := tor.Request(cons.PeerId); err != nil {
				retryCount++
				if retryCount >= MaxRetryCount {
					comController.SendParent(com.TotalFailure, nil, err, nil, childId)
					return
				} else {
					comController.SendParent(com.Failure, nil, err, nil, childId)
				}
			} else {
				retryCount = 0
			}

			// Reset timer
			intervalTimer = time.NewTimer(time.Duration(tor.Tracker.Interval))
		}
	}

}
