package internal

import (
	"fmt"
	"math/rand"
	"path/filepath"
	"strings"
	"time"

	"github.com/jmatss/torc/internal/torrent"
	"github.com/jmatss/torc/internal/util/com"
	"github.com/jmatss/torc/internal/util/cons"
	"github.com/jmatss/torc/internal/util/logger"
)

func Controller(comView com.Channel, childId string) {
	cons.DownloadPath = fetchDownloadPathFromDisk()
	cons.PeerId = newPeerId()

	comView.AddChild(childId)
	defer comView.RemoveChild(childId)

	// Spawn handlers. Every handler will be in charge of a specific torrent with
	// the InfoHash of the torrent being used as the "childId" in the com.Channel.
	comTorrentHandler := com.New()
	for _, tor := range fetchTorrentsFromDisk() {
		go TorrentHandler(comTorrentHandler, tor)
	}

	for {
		select {
		case received := <-comView.GetChildChannel(childId):
			/*
				Received message from "view"/parent.
			*/
			switch received.Id {
			case com.Add:
				if received.Torrent == nil {
					comView.SendParent(
						com.Failure,
						nil,
						fmt.Errorf("no torrent specified when trying to \"%s\"", received.Id.String()),
						nil,
						childId,
					)
					break
				}

				handlerId := string(received.Torrent.Tracker.InfoHash[:])
				if comTorrentHandler.Exists(handlerId) {
					comView.SendParent(
						com.Failure,
						nil,
						fmt.Errorf("tried to add a torrent that already exists"),
						nil,
						childId,
					)
					break
				}

				// TODO: Might have to do a synchronized send and receive so that
				//  the client can be notified if it succeeded/failed immediately.
				go TorrentHandler(comTorrentHandler, received.Torrent)

			case com.Remove, com.Start, com.Stop:
				// TODO: Fix this, must send correct child id (InfoHash).
				if ok := comTorrentHandler.SendChild(received.Id, nil, nil, nil, childId); !ok {
					comView.SendParent(
						com.Failure,
						nil,
						fmt.Errorf("tried to \"%s\" non existing torrent", received.Id),
						nil,
						childId,
					)
				}

			case com.Quit, com.List:
				comTorrentHandler.SendChildren(received.Id, nil)

			case com.LogLevel:
				err := setLogLevel(string(received.Data))
				comView.SendParent(
					com.LogLevel,
					nil,
					err,
					nil,
					childId,
				)
			}

		case received := <-comTorrentHandler.Parent:
			/*
				Received message from one of the "handlers"/children.
			*/
			switch received.Id {
			case com.Add, com.Remove, com.Start, com.Stop, com.List:
				// The torrentHandler has executed the commands sent from the view.
				// Just pass along to the view so it can see the results.
				comView.SendParentCopy(received, childId)

			}
		}
	}
}

func newPeerId() string {
	// Generate peer id. Format: -<client id(2 bytes)><version(4 bytes)>-<12 random ascii numbers>
	// use: client id 'UT'(µTorrent) and random version for anonymity
	peerId := make([]byte, 20)
	peerId[0], peerId[1], peerId[2], peerId[7] = '-', 'U', 'T', '-'

	rand.Seed(time.Now().UTC().UnixNano())
	for i := 3; i < len(peerId); i++ {
		if i == 7 {
			continue
		}
		// (ascii 48 -> 57) => (0 -> 9)
		peerId[i] = byte(rand.Intn(10) + 48)
	}

	return string(peerId)
}

// TODO: currently only returns empty
func fetchTorrentsFromDisk() map[string]*torrent.Torrent {
	return make(map[string]*torrent.Torrent, 0)
}

// TODO: currently only returns default
func fetchDownloadPathFromDisk() string {
	return filepath.FromSlash("")
}

func setLogLevel(level string) error {
	for i, realLevel := range logger.GetValues() {
		if strings.ToLower(level) == strings.ToLower(realLevel) {
			logger.CurrentLevel = logger.Level(i)
			return nil
		}
	}

	return fmt.Errorf("unable to set log level to \"%s\"", level)
}
