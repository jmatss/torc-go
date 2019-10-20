package torrent

import (
	"bytes"
	"crypto/sha1"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/jmatss/torc/internal/peer"
)

const (
	// Enum for "event" flag on tracker requests
	Interval = iota
	Started
	Stopped
	Completed

	UserAgent = "torc/1.0"
)

type EventId int

func (id EventId) String() string {
	return []string{
		"Interval",
		"Started",
		"Stopped",
		"Completed",
	}[id]
}

type Tracker struct {
	sync.Mutex

	InfoHash   [sha1.Size]byte
	Uploaded   int64
	Downloaded int64
	Left       int64

	// Contains the bitfield of the pieces that this client have downloaded
	// and can be seeded to other clients.
	BitFieldHave []byte

	// Contains the bitfield of the pieces that this client have downloaded
	// and also the pieces that the peerHandlers are currently downloading.
	// This can be used to see which pieces that are free to start downloading.
	BitFieldDownloading []byte

	Started   bool
	Completed bool

	Interval int64
	Seeders  int64 `bencode:"complete"`
	Leechers int64 `bencode:"incomplete"`
	Peers    map[string]*peer.Peer
}

// Creates a new tracker struct that will contain anything tracker related
// including all peers.
func NewTracker(content []byte, tor *Torrent) error {
	tracker := Tracker{}

	// Get "info" field from the bencoded torrent
	// and sha1 hash it into tracker.InfoHash.
	info, err := GetDictValue(content, "info")
	if err != nil {
		return fmt.Errorf("unable to get field \"info\" from "+
			"torrent file: %w", err)
	}
	tracker.InfoHash = sha1.Sum(info)

	var left int64 = 0
	for _, file := range tor.Files {
		left += file.Length
	}
	tracker.Left = left

	// Pieces will be divisible by 20 (sha1.Size)
	// Bitfield initialized to all zeros
	bitFieldLength := ((len(tor.Pieces) / sha1.Size) / 8) + 1
	tracker.BitFieldHave = make([]byte, bitFieldLength)
	tracker.BitFieldDownloading = make([]byte, bitFieldLength)
	for i := 0; i < bitFieldLength; i++ {
		tracker.BitFieldHave[i] = 0
		tracker.BitFieldDownloading[i] = 0
	}

	// Uploaded, Downloaded, Interval, Seeders and Leecehers initialized to 0
	// Started and Completed initialized to false
	// Peers initialized to nil
	tor.Tracker = tracker

	return nil
}

// Used when doing either the first request to the tracker
// or doing a regular "interval" request.
func (t *Torrent) Request(peerId string) error {
	if t.Tracker.Completed {
		return fmt.Errorf("this torrent has already finished downloading")
	} else if !t.Tracker.Started {
		return t.trackerRequest(peerId, Started)
	} else {
		return t.trackerRequest(peerId, Interval)
	}
}

// Used to send a message to the tracker to indicate that this client
// will stop requesting data.
func (t *Torrent) Stop(peerId string, completed bool) error {
	if completed {
		return t.trackerRequest(peerId, Completed)
	} else {
		return t.trackerRequest(peerId, Stopped)
	}
}

func (t *Torrent) trackerRequest(peerId string, event EventId) error {
	params := url.Values{}
	params.Add("info_hash", string(t.Tracker.InfoHash[:]))
	params.Add("peer_id", peerId)
	params.Add("port", strconv.Itoa(Port))
	params.Add("uploaded", strconv.Itoa(int(t.Tracker.Uploaded)))
	params.Add("downloaded", strconv.Itoa(int(t.Tracker.Downloaded)))
	params.Add("left", strconv.Itoa(int(t.Tracker.Left)))
	params.Add("compact", "1")
	params.Add("no_peer_id", "1")

	switch event {
	case Interval:
		// Should be set during regular "interval" calls to the tracker
		// , no need to set the "event" flag
	case Started, Stopped, Completed:
		if event == Started {
			t.Tracker.Started = true
		} else if event == Completed {
			t.Tracker.Completed = true
		}

		params.Add("event", strings.ToLower(event.String()))
	default:
		return fmt.Errorf("incorrect \"event\" set during tracker request")
	}

	// See whether this is the first parameter or if there are other parameters
	// already added into the "Announce" url.
	var URL string
	if strings.Contains(t.Announce, "?") {
		URL = t.Announce + "&" + params.Encode()
	} else {
		URL = t.Announce + "?" + params.Encode()
	}

	client := &http.Client{}
	request, err := http.NewRequest("GET", URL, nil)
	if err != nil {
		return fmt.Errorf("unable to create new htto request "+
			"for url %s: %w", URL, err)
	}
	request.Header.Set("User-Agent", UserAgent)

	resp, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("unable to connect to %s: %w", URL, err)
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("unable to read response from %s: %w", URL, err)
	}

	// If the response body contains the key "failure reason", the tracker request failed.
	if bytes.Contains(body, []byte("failure reason")) {
		reason, err := GetString(body, "failure reason")
		if err != nil {
			return err
		} else {
			return fmt.Errorf("received failure reason from remote peer: %s", reason)
		}
	}

	// TODO: uglier code while using interfaces and casting over and over again,
	//  but would be better performance compared to current code using bytes and
	//  looking through the bytes in linear time.
	// Get interval, seeders(complete) and leechers(incomplete) from the tracker response
	interval, err := GetInt(body, "interval")
	if err != nil {
		return err
	}
	seeders, err := GetInt(body, "complete")
	if err != nil {
		return err
	}
	leechers, err := GetInt(body, "incomplete")
	if err != nil {
		return err
	}

	t.Tracker.Interval = interval
	t.Tracker.Seeders = seeders
	t.Tracker.Leechers = leechers

	peers, err := getPeers(body)
	if err != nil {
		return err
	}

	// If true: first "contact" with the tracker, i.e. all received peers are new,
	//	        add all of them to the the tracker struct.
	// Else: add all new peers that isn't already among the "old" peers
	if t.Tracker.Peers == nil {
		t.Tracker.Peers = peers
	} else {
		for _, newPeer := range peers {
			if _, ok := t.Tracker.Peers[newPeer.HostAndPort]; !ok {
				t.Tracker.Peers[newPeer.HostAndPort] = newPeer
			}
		}
	}

	return nil
}
