package torrent

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"unicode"

	"github.com/jackpal/bencode-go"
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
	InfoHash   [sha1.Size]byte
	Uploaded   int64
	Downloaded int64
	Left       int64
	BitField   []byte

	Started   bool
	Completed bool

	Interval int64
	Seeders  int64 `bencode:"complete"`
	Leechers int64 `bencode:"incomplete"`
	Peers    []Peer
}

// Creates a new tracker struct that will contain anything tracker related
// including all peers.
func newTracker(torrent *Torrent, file *os.File) error {
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
	for _, file := range torrent.Info.Files {
		left += file.Length
	}
	tracker.Left = left

	// bitfield initialized to all zeros
	bitFieldLength := ((len(torrent.Info.Files) - 1) / 8) + 1
	tracker.BitField = make([]byte, bitFieldLength)

	// Uploaded, Downloaded, Interval, Seeders and Leecehers initialized to 0
	// Started and Completed initialized to false
	// Peers initialized to nil
	torrent.Tracker = tracker

	return nil
}

// Used when doing either the first request to the tracker
// or doing a regular "interval" request.
func (t *Torrent) Request(peerId string) error {
	if t.Tracker.Completed {
		return fmt.Errorf("this torrent has already finished downloading")
	} else if !t.Tracker.Started {
		return t.TrackerRequest(peerId, Started)
	} else {
		return t.TrackerRequest(peerId, Interval)
	}
}

// Used to send a message to the tracker to indicate that this client
// will stop requesting data.
func (t *Torrent) Stop(peerId string, completed bool) error {
	if completed {
		return t.TrackerRequest(peerId, Completed)
	} else {
		return t.TrackerRequest(peerId, Stopped)
	}
}

func (t *Torrent) TrackerRequest(peerId string, event EventId) error {
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
			"the tracker: %w", err)
	}

	peers, err := getPeers(body)
	if err != nil {
		return err
	}
	t.Tracker.Peers = peers

	return nil
}

/*
	Tracker Response dictionary model:
		d
			14:failure reason <num>:<string>
			8:interval i<num>e
			8:complete i<num>e		(seeders)
			10:incomplete i<num>e	(leechers)
			5:peers l
				d
					2:ip <num>:<string>		// IPv6 (hexed), IPv4 (dotted quad) or DNS name (string)
					4:port i<num>e
				e
				...
			e
		e

	Tracker Response binary model:
		d
			14:failure reason <num>:<string>
			8:interval i<num>e
			8:complete i<num>e		(seeders)
			10:incomplete i<num>e	(leechers)
			5:peers <num>:<string>	// <string> contains a multiple of 6 bytes (IP(4) + PORT(2)) ...
		e
*/
// Get peers from the tracker response either in the dictionary or the binary model.
func getPeers(body []byte) ([]Peer, error) {
	// Get the bencoded value with the key "peers".
	// Is either a list or a string
	peersValue, err := getValue(body, "peers")
	if err != nil {
		return nil, err
	}

	startIndex := 0
	currentIndex := startIndex // will be incremented
	var peers []Peer

	if peersValue[currentIndex] == 'l' {
		/*
			Dictionary model
		*/
		peersReader := bytes.NewReader(peersValue)
		peersList, err := bencode.Decode(peersReader)
		if err != nil {
			return nil, err
		}

		peersListInterface, ok := peersList.([]interface{})
		if !ok {
			return nil, fmt.Errorf("unable to convert the \"peers\" value " +
				"into a list using the dictionary model")
		}

		peers = make([]Peer, 0, len(peersListInterface))

		// TODO: need testing for this. IPv4, IPv6 & hostname
		for _, peersInterface := range peersListInterface {
			peersMap, ok := peersInterface.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("unable to convert the \"peers\" value " +
					"into a map using the dictionary model")
			}

			ipInterface, ok := peersMap["ip"]
			if !ok {
				return nil, fmt.Errorf("unable to find the \"ip\" field " +
					"inside the \"peers\" map using the dictionary model")
			}

			ipString, ok := ipInterface.(string)
			if !ok {
				return nil, fmt.Errorf("unable to convert the \"ip\" field " +
					"into string using the dictionary model")
			}

			portInterface, ok := peersMap["port"]
			if !ok {
				return nil, fmt.Errorf("unable to find the \"port\" field " +
					"inside the \"peers\" map using the dictionary model")
			}

			port, ok := portInterface.(int64)
			if !ok {
				return nil, fmt.Errorf("unable to convert the \"port\" field " +
					"from string into int using the dictionary model")
			}

			peers = append(peers, NewPeer(ipString, port))
		}

	} else {
		/*
			Binary model, the peers will be grouped into 6 bytes (4 bytes IP + 2 bytes PORT)
			concatenated together in a "byte string".
		*/
		groupLen := 6

		if unicode.IsDigit(rune(peersValue[currentIndex])) {
			for peersValue[currentIndex] != ':' {
				currentIndex++
			}

			stringLength, err := strconv.Atoi(string(peersValue[startIndex:currentIndex]))
			if err != nil {
				return nil, err
			}
			if stringLength%groupLen != 0 {
				return nil, fmt.Errorf("value of the \"peers\" field in the received "+
					"binary model from the tracker is not divisible by %d, actual length: %d",
					groupLen, stringLength)
			}

			// increment current index to point to after the colon and at the start index of the "text" string
			currentIndex++

			amountOfPeers := stringLength / groupLen
			peers = make([]Peer, 0, amountOfPeers)

			for i := 0; i < amountOfPeers; i++ {
				ip := net.IPv4(
					peersValue[currentIndex+(i*groupLen+0)],
					peersValue[currentIndex+(i*groupLen+1)],
					peersValue[currentIndex+(i*groupLen+2)],
					peersValue[currentIndex+(i*groupLen+3)],
				)

				portBytes := peersValue[currentIndex+(i*groupLen+4) : currentIndex+(i*groupLen+6)]
				port := int64(binary.BigEndian.Uint16(portBytes))

				peers = append(peers, NewPeerIp(ip, port))
			}

		} else {
			return nil, fmt.Errorf("incorrect format of the field \"peers\" "+
				"from a binary model tracker, expected: string, got: %q", rune(peersValue[currentIndex]))
		}
	}

	return peers, nil
}

// Gets the value stored in the bencoded dictionary with the key "key".
func getValue(content []byte, key string) (result []byte, err error) {
	keyLen := strconv.Itoa(len(key))
	pattern := []byte(keyLen + ":" + key)

	index := bytes.Index(content, pattern)
	if index == -1 {
		return nil, fmt.Errorf("unable to find the \"%s\" field in the torrent file", key)
	}

	// Keeps track of how many "structures" (dict or list) that have been started and not ended.
	// Ex. d3:abcli32e would have count 2 since the dictionary("d") and the list("l") haven't been
	// ended with an "e" yet. When the counter goes down to 0 again,
	// the end of the "wrapping" structure has been reached.
	openCount := 0

	// put startIndex at next byte after "keyLen + ":" + key"
	startIndex := index + len(pattern)
	currentIndex := startIndex

	// Loop through and find the index of the "e" that ends the wrapping structure,
	// i.e. the end of the value that is to be returned.
	// Then return a byte slice of [startIndex:"index of the ending e" + 1]
	//
	// Might loop until end of "content" and get a index out of bounds if the
	// torrent file has incorrect format, recover if that is the case.
	defer func() {
		if r := recover(); r != nil {
			result = nil
			err = fmt.Errorf("unable to parse the \"%s\" field in the torrent file, "+
				"the field never ended with an \"e\": %w", key, r.(error))
		}
	}()
	for {
		switch current := content[currentIndex]; current {
		case 'd', 'l':
			openCount++
			currentIndex++
		case 'e':
			openCount--
			currentIndex++
			if openCount < 0 {
				return nil, fmt.Errorf("incorrect format of torrent file, "+
					"received an ending \"e\" while no structure have been \"opened\" "+
					"while parsing the \"%s\" field", key)
			}
		case 'i':
			for content[currentIndex] != 'e' {
				currentIndex++
			}
			currentIndex++
		default:
			if unicode.IsDigit(rune(current)) {
				// will "skip" the string and currentIndex will point to the next "structure"
				_, currentIndex, err = getStringIndices(content, currentIndex)
				if err != nil {
					return nil, fmt.Errorf("error while parsing the "+
						"\"%s\" field: %w", key, err)
				}
			} else {
				return nil, fmt.Errorf("incorrect format of torrent file, "+
					"expecting start of: dict, list, int or string, got: %q", rune(current))
			}
		}

		if openCount == 0 {
			break
		}
	}

	return content[startIndex:currentIndex], nil
}

// Test and see if the response from the tracker contains a dictionary entry
// with the key "key" and a string as value.
// If it does: return the corresponding string value and an bool indicating
// that the value exists, else: return a bool that indicates that it does not exist
func getValueString(content []byte, key string) (string, bool, error) {
	keyLen := strconv.Itoa(len(key))
	pattern := []byte(keyLen + ":" + key)

	index := bytes.Index(content, pattern)
	if index == -1 {
		// The torrent file does not contain a field with the key "key"
		return "", false, nil
	}

	resultBytes, err := getValue(content, key)
	if err != nil {
		return "", true, err
	}

	start, end, err := getStringIndices(resultBytes, 0)
	if err != nil {
		return "", true, err
	}

	return string(resultBytes[start:end]), true, nil
}

// Returns the index to the first byte of the "byte string" and
// the index of the byte just after the "given" bencoded string.
// Example, a call with these arguments:
//  content = 5:abcdei3e
//  index 	= 0
// would return 2, 7 and nil
func getStringIndices(content []byte, index int) (int, int, error) {
	startIndex := index
	for content[index] != ':' {
		index++
	}

	stringLength, err := strconv.Atoi(string(content[startIndex:index]))
	if err != nil {
		return 0, 0, fmt.Errorf("unable to convert benocded string length"+
			"to integer: %w", err)
	}

	stringStart := index + 1
	stringEnd := startIndex + (index - startIndex) + 1 + stringLength
	return stringStart, stringEnd, nil
}
