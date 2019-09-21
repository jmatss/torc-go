package client

import (
	"log"
	"math/rand"
	"path/filepath"
	"time"

	"github.com/jmatss/torc/internal/client/torrent"
)

type Client struct {
	PeerId string
}

func newClient() Client {
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

	return Client{
		PeerId: string(peerId),
	}
}

func Run(torrentFile string) {
	client := newClient()

	filename := filepath.FromSlash(torrentFile)
	tor, err := torrent.NewTorrent(filename)
	if err != nil {
		log.Fatalf("%v", err)
	}

	err = tor.Request(client.PeerId)
	if err != nil {
		log.Printf("ERROR: %v", err)
	}

	/*
		log.Printf("Announce: %v", tor.Announce)
		log.Printf("Info.Name: %v", tor.Info.Name)
		log.Printf("Info.PieceLength: %v", tor.Info.PieceLength)
		for _, file := range tor.Info.Files {
			log.Printf(" - file length: %v", file.Length)
			log.Printf(" - file path: %v\n", file.Path)
		}
		log.Printf("Info.Files len: %v", len(tor.Info.Files))
		log.Printf("peer id: %s", client.PeerId)
		log.Printf("InfoHash: %040x", tor.Tracker.InfoHash)
	*/

	log.Printf("Started: %t", tor.Tracker.Started)
	log.Printf("Completed: %t", tor.Tracker.Completed)
	log.Printf("Interval: %d", tor.Tracker.Interval)
	log.Printf("Seeders: %d", tor.Tracker.Seeders)
	log.Printf("Leecehers: %d", tor.Tracker.Leechers)
	for _, peer := range tor.Tracker.Peers {
		if peer.UsingIp {
			log.Printf(" - IpPort: %v:%d", peer.Ip, peer.Port)
		} else {
			log.Printf(" - HostnamePort: %s:%d", peer.Hostname, peer.Port)
		}
	}

	err = tor.Stop(client.PeerId, false)
	if err != nil {
		log.Printf("ERROR: %v", err)
	}
}
