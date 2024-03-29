package torrent

import (
	"fmt"
	"time"

	"github.com/jmatss/torc/internal/peer"
	"github.com/jmatss/torc/internal/util/com"
	"github.com/jmatss/torc/internal/util/cons"
	"github.com/jmatss/torc/internal/util/logger"
)

const (
	// Max retries before it gives up on the tracker
	// Will wait tracker.Interval seconds between retries.
	MaxRetryCount = 5
	MaxPeers      = 8
)

// Handler in charge of one specific torrent.
// TODO: setup so that other peers can connect to this handler
func Handler(comController com.Channel, tor *Torrent) {
	childId := string(tor.Tracker.InfoHash[:])

	logger.Log(logger.Low, "torrent.Handler started")

	// Make tracker request. This handler will kill itself if it isn't able to
	// complete the tracker request.
	if err := tor.Request(cons.PeerId); err != nil {
		comController.SendParent(com.Add, nil, err, tor, childId)
		return
	}
	comController.SendParent(com.Add, nil, nil, tor, childId)
	comController.AddChild(childId)
	defer comController.RemoveChild(childId)

	logger.Log(logger.High, "torrent.handler tracker request done successfully")

	// Start up peerHandlers. Every peer handler will be in charge of one peer
	// of this torrent.
	comPeerHandler := com.New()
	{
		i := 0
		for _, val := range tor.Tracker.Peers {
			if i >= MaxPeers {
				break
			}
			go peer.Handler(comPeerHandler, val, tor)
			i++
		}
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
					comController.SendParentError(
						received.Id,
						fmt.Errorf("cant start since it isn't stopped, %d go processes "+
							"still running", count),
					)
				} else {
					i := 0
					for _, val := range tor.Tracker.Peers {
						if i >= MaxPeers {
							break
						}
						go peer.Handler(comPeerHandler, val, tor)
						i++
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
				// Is selected ~random (depends on the implementation of go's range loop)
				for _, val := range tor.Tracker.Peers {
					if !comPeerHandler.Exists(val.HostAndPort) {
						go peer.Handler(comPeerHandler, val, tor)
						break
					}
				}
			case com.Failure:
			// TODO: log
			default:
				// TODO: log
			}

		case <-intervalTimer.C:
			/*
				Interval time expired. Send new tracker request to get updated information.
			*/
			// TODO: Start connection to peers if it is needed.

			logger.Log(logger.High, "torrent.Handler interval timeout")

			if err := tor.Request(cons.PeerId); err != nil {
				retryCount++
				if retryCount >= MaxRetryCount {
					comController.SendParentError(com.TotalFailure, err)
					return
				} else {
					comController.SendParentError(com.Failure, err)
				}
			} else {
				retryCount = 0
			}

			// Reset timer
			intervalTimer = time.NewTimer(time.Duration(tor.Tracker.Interval) * time.Second)
		}
	}

}
