package torrent

import (
	"bytes"
	"crypto/sha1"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/jackpal/bencode-go"
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
	Peers    []peer.Peer
}

// Creates a new tracker struct that will contain anything tracker related
// including all peers.
func NewTracker(tor *Torrent, file *os.File) error {
	tracker := Tracker{}

	content, err := ioutil.ReadAll(file)
	if err != nil {
		return fmt.Errorf("unable to read from torrent file %s: %w",
			file.Name(), err)
	}

	// Get "info" field from the bencoded torrent
	// and sha1 hash it into tracker.InfoHash.
	info, err := getValue(content, "info")
	if err != nil {
		return fmt.Errorf("unable to get field \"info\" from "+
			"torrent file %s: %w", file.Name(), err)
	}
	tracker.InfoHash = sha1.Sum(info)

	var left int64 = 0
	for _, file := range tor.Info.Files {
		left += file.Length
	}
	tracker.Left = left

	// Pieces will be divisible by 20 (sha1.Size)
	// Bitfield initialized to all zeros
	bitFieldLength := ((len(tor.Info.Pieces) / sha1.Size) / 8) + 1
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

	reason, failure, err := getValueString(body, "failure reason")
	if err != nil {
		return fmt.Errorf("error while trying to decode "+
			"failure reason from tracker request: %w", err)
	} else if failure {
		return fmt.Errorf("received failure from tracker request: %s", reason)
	}

	// Get interval, seeders(complete) and leechers(incomplete) from the tracker response
	err = bencode.Unmarshal(bytes.NewReader(body), &t.Tracker)
	if err != nil {
		return fmt.Errorf("unable to unmarshal the response body into "+
			"the tracker struct: %w", err)
	} else if t.Tracker.Interval <= 0 {
		return fmt.Errorf("received tracker response has interval time "+
			"less than or equal zero", err)
	}

	peers, err := getPeers(body)
	if err != nil {
		return err
	}

	// If true: first "contact" with the tracker, i.e. all received peers are new,
	//	        add all of them to the the tracker struct.
	// Else: append all new peers that doesn't already exist in the "old" peers
	if t.Tracker.Peers == nil {
		t.Tracker.Peers = peers
	} else {
		var alreadyExists bool
		for _, newPeer := range peers {
			alreadyExists = false
			for _, oldPeer := range t.Tracker.Peers {
				if oldPeer.Equal(&newPeer) {
					alreadyExists = true
					break
				}
			}

			if !alreadyExists {
				t.Tracker.Peers = append(t.Tracker.Peers, newPeer)
			}
		}
	}

	return nil
}
