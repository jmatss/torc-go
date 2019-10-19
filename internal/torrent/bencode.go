package torrent

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"strconv"
	"unicode"

	"github.com/jackpal/bencode-go"
	"github.com/jmatss/torc/internal/peer"
)

/*
	Structure of torrent file with a single file:
		map(
			announce: -
			info:
				map(
					name: -
					piece length: -
					pieces: -
					length: -
				)
		)

	Structure of a torrent file with multiple files:
		map(
			announce: -
			info:
				map(
					name: -
					piece length: -
					pieces: -
					files:
						list(
							map(
								length: -
								path: -
							)
							...
						)
				)
		)
*/
// Fetch, convert and return the "files" field from the torrent file.
// If it is a single file torrent, convert it to a "multiple files structure"
// with only one item in the "Files" slices.
func getFiles(file *os.File) ([]Files, error) {
	infoMap, err := getInfoMap(file)
	if err != nil {
		return nil, err
	}

	// If the length field exists: this is a single file torrent.
	// Else: it is a multiple files torrent. (see comment on format over this function)
	lengthInterface, ok := infoMap["length"]
	files := make([]Files, 0, 1)

	if ok {
		/*
			Single file torrent
		*/
		if length, ok := lengthInterface.(int64); ok {
			if nameInterface, ok := infoMap["name"]; ok {
				if name, ok := nameInterface.(string); ok {
					// Reuse the "name" field as "path"
					files = append(
						files,
						Files{
							Index:  0,
							Length: length,
							Path:   []string{name},
						},
					)
					return files, nil
				}
			}
		}

		return nil, fmt.Errorf("bencoded torrent file incorrect format: " +
			"unable to parse the \"length\" and/or \"name\" field")
	} else {
		/*
			Multiple files torrent
		*/
		var totalLength int64 = 0

		filesInterface, ok := infoMap["files"]
		if !ok {
			return nil, fmt.Errorf("bencoded torrent file incorrect format: " +
				"couldn't find field \"files\"")
		}

		filesInterfaceSlice, ok := filesInterface.([]interface{})
		if !ok {
			return nil, fmt.Errorf("bencoded torrent file incorrect format: " +
				"expected \"files\" field to contain a list")
		}

		// Loop through all files and create new "Files" with their length and path
		// that is appended to the "files" slice.
		for _, fileInterface := range filesInterfaceSlice {
			fileMap, ok := fileInterface.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("bencoded torrent file incorrect format: " +
					"expected the list in \"files\" to contain maps")
			}

			lengthInterface, ok := fileMap["length"]
			if !ok {
				return nil, fmt.Errorf("bencoded torrent file incorrect format: " +
					"expected a \"length\" field inside a file inside the \"files\" field")
			}

			length, ok := lengthInterface.(int64)
			if !ok {
				return nil, fmt.Errorf("bencoded torrent file incorrect format: " +
					"expected \"length\" field inside the \"files\" field to contain an int")
			}

			pathSlice, ok := fileMap["path"]
			if !ok {
				return nil, fmt.Errorf("bencoded torrent file incorrect format: " +
					"expected a \"path\" field inside a file inside the \"files\" field")
			}

			pathSliceInterface, ok := pathSlice.([]interface{})
			if !ok {
				return nil, fmt.Errorf("bencoded torrent file incorrect format: " +
					"expected \"path\" field inside the \"files\" field to contain slices of interface")
			}

			path := make([]string, 0, 1)

			for _, pathInterface := range pathSliceInterface {
				pathString, ok := pathInterface.(string)
				if !ok {
					return nil, fmt.Errorf("bencoded torrent file incorrect format: " +
						"expected \"path\" field inside the \"files\" field to contain string slices")
				}

				path = append(path, pathString)
			}

			files = append(
				files,
				Files{
					Index:  totalLength,
					Length: length,
					Path:   path,
				},
			)

			totalLength += length
		}
	}

	return files, nil
}

// Get the "info" field containing a map from the torrent file.
// The value of the info map will be the input to the sha1 algorithm to
// create the "info hash" of the torrent.
func getInfoMap(file *os.File) (map[string]interface{}, error) {
	torrentInterface, err := bencode.Decode(file)
	if err != nil {
		return nil, fmt.Errorf("unable to bencode decode torrent file: %w", err)
	}

	if torrentMap, ok := torrentInterface.(map[string]interface{}); ok {
		if infoInterface, ok := torrentMap["info"]; ok {
			if infoMap, ok := infoInterface.(map[string]interface{}); ok {
				return infoMap, nil
			}
		}
	}
	return nil, fmt.Errorf("bencoded torrent file incorrect format: " +
		"unable to parse the \"info\" field")
}

/*
	Tracker Error dictionary model:
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

	Tracker Error binary model:
		d
			14:failure reason <num>:<string>
			8:interval i<num>e
			8:complete i<num>e		(seeders)
			10:incomplete i<num>e	(leechers)
			5:peers <num>:<string>	// <string> contains a multiple of 6 bytes (IP(4) + PORT(2)) ...
		e
*/
// Get peers from the tracker response either in the dictionary or the binary model.
func getPeers(body []byte) ([]peer.Peer, error) {
	// Get the bencoded value with the key "peers".
	// Is either a list or a string
	peersValue, err := getValue(body, "peers")
	if err != nil {
		return nil, err
	}

	startIndex := 0
	currentIndex := startIndex // will be incremented
	var peers []peer.Peer

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

		peers = make([]peer.Peer, 0, len(peersListInterface))

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

			peers = append(peers, peer.NewPeer(ipString, port))
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
			peers = make([]peer.Peer, 0, amountOfPeers)

			for i := 0; i < amountOfPeers; i++ {
				ip := net.IPv4(
					peersValue[currentIndex+(i*groupLen+0)],
					peersValue[currentIndex+(i*groupLen+1)],
					peersValue[currentIndex+(i*groupLen+2)],
					peersValue[currentIndex+(i*groupLen+3)],
				)

				portBytes := peersValue[currentIndex+(i*groupLen+4) : currentIndex+(i*groupLen+6)]
				port := int64(binary.BigEndian.Uint16(portBytes))

				peers = append(peers, peer.NewPeer(ip.String(), port))
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
