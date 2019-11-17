package torrent

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"strconv"
	"unicode"

	"github.com/jmatss/torc/internal/peer"
)

// Error used to indicate that a specified key doesn't exist in the dictionary.
type NotFoundError struct{ msg string }

func (e *NotFoundError) Error() string { return e.msg }

// Decode the specified "bencoded structures" recursively to their corresponding "go structures".
// Everything is casted into interface{} before being returned.
//
// Conversion _Bencode_ -> _Go_:
//  dictionary -> map[string]interface{}
//  list -> []interface{}
//  string -> string
//  integer -> int64
func Decode(content []byte) (res interface{}, err error) {
	if len(content) < 2 {
		return nil, fmt.Errorf("given value is to small, "+
			"expected: >2, got: %d", len(content))
	}

	// Might loop until end of "content" and get a index out of bounds if the
	// list has incorrect format, recover if that is the case.
	defer func() {
		if r := recover(); r != nil {
			res = nil
			err = fmt.Errorf("unable to parse the list during decoding, "+
				"the list never ended with an \"e\": %w", r.(error))
		}
	}()

	switch content[0] {
	case 'd':
		result, err := GetDictInterfaces(content)
		if err != nil {
			return nil, err
		}

		return result, err

	case 'l':
		resultList := make([]interface{}, 0)
		// Remove starting "l" and ending "e".
		content := content[1 : len(content)-1]

		for {
			resultBytes, err := GetNext(content)
			if err != nil {
				return nil, err
			}

			result, err := Decode(resultBytes)
			if err != nil {
				return nil, err
			}
			resultList = append(resultList, result)

			content = content[len(resultBytes):]
			if len(content) <= 0 {
				break
			}
		}

		return resultList, nil

	case 'i':
		currentIndex := 0
		for content[currentIndex] != 'e' {
			currentIndex++
		}

		result, err := strconv.ParseInt(string(content[1:currentIndex]), 10, 64)
		if err != nil {
			return nil, err
		}

		return result, nil

	default:
		if unicode.IsDigit(rune(content[0])) {
			start, end, err := getStringIndices(content)
			if err != nil {
				return nil, err
			}
			result := string(content[start:end])

			return result, nil
		} else {
			return nil, fmt.Errorf("incorrect format of torrent file, "+
				"expecting start of: dict, list, int or string, got: %q", rune(content[0]))
		}
	}
}

// Returns the next "structure" contained in the "content" byte slice.
// A "structure" is either a dictionary, list, string or integer.
func GetNext(content []byte) (result []byte, err error) {
	// Might loop until end of "content" and get a index out of bounds if the
	// torrent file has incorrect format, recover if that is the case.
	defer func() {
		if r := recover(); r != nil {
			result = nil
			err = fmt.Errorf("unable to parse the next field in the torrent file, "+
				"the field never ended with an \"e\": %w", r.(error))
		}
	}()

	// "openCount" keeps track of how many "structures" (dict or list) that have been started
	// and not ended. Ex. d3:abcli32e would have count 2 since the dictionary("d") and the
	// list("l") haven't been ended with an "e" yet. When the counter goes down to 0 again,
	// the end of the "structure" has been reached and it's time to return.
	openCount := 0

	// Loop through and find the end of the "structure" (dict, list, string or int).
	// Return the structure as a byte slice [startIndex:currentIndex].
	startIndex := 0
	currentIndex := 0
	for {
		switch current := content[currentIndex]; current {
		case 'd', 'l':
			openCount++
			currentIndex++
		case 'i':
			for content[currentIndex] != 'e' {
				currentIndex++
			}
			currentIndex++
		case 'e':
			openCount--
			currentIndex++
			if openCount < 0 {
				return nil, fmt.Errorf("incorrect format of torrent file, " +
					"received an ending \"e\" while no structure have been \"opened\"")
			}
		default:
			if unicode.IsDigit(rune(current)) {
				// will "skip" the string and currentIndex will point to the next "structure"
				_, currentIndex, err = getStringIndices(content[currentIndex:])
				if err != nil {
					return nil, err
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

// Returns the value as bytes stored in the bencoded dictionary with the key "key".
// Returns a NotFoundError if the key doesn't exist.
func GetDictValue(content []byte, key string) ([]byte, error) {
	keyLen := strconv.Itoa(len(key))
	pattern := []byte(keyLen + ":" + key)

	index := bytes.Index(content, pattern)
	if index == -1 {
		return nil, &NotFoundError{
			fmt.Sprintf("unable to find the \"%s\" field in the torrent file", key),
		}
	}

	result, err := GetNext(content[index+len(pattern):])
	if err != nil {
		return nil, err
	}

	return result, nil
}

// Gets the decoded value stored in the bencoded dictionary with the key "key".
// Returns a NotFoundError if the key doesn't exist.
func GetDictInterface(content []byte, key string) (interface{}, error) {
	keyLen := strconv.Itoa(len(key))
	pattern := []byte(keyLen + ":" + key)

	index := bytes.Index(content, pattern)
	if index == -1 {
		return nil, &NotFoundError{
			fmt.Sprintf("unable to find the \"%s\" field in the torrent file", key),
		}
	}

	valueBytes, err := GetNext(content[index+len(pattern):])
	if err != nil {
		return nil, err
	}

	value, err := Decode(valueBytes)
	if err != nil {
		return nil, err
	}

	return value, nil
}

// Fetches all key:value pairs from the bencoded dictionary inside "content"
// and returns them as a map[string]interface{}.
func GetDictInterfaces(content []byte) (map[string]interface{}, error) {
	if len(content) < 3 {
		return nil, fmt.Errorf("incorrect format of given content")
	}

	// Remove dictionary starting "d" and ending "e".
	content = content[1 : len(content)-1]
	result := make(map[string]interface{}, 0)

	offset := 0
	for {
		start, end, err := getStringIndices(content[offset:])
		if err != nil {
			return nil, err
		}
		key := string(content[start:end])

		valueBytes, err := GetNext(content[end:])
		if err != nil {
			return nil, err
		}

		value, err := Decode(valueBytes)
		if err != nil {
			return nil, err
		}

		result[key] = value

		offset += len(valueBytes)
		if offset >= len(content) {
			break
		}
	}

	return result, nil
}

// Gets the integer stored in the bencoded dictionary with the key "key".
// Returns a NotFoundError if the key doesn't exist.
func GetInt(content []byte, key string) (int64, error) {
	resultBytes, err := GetDictValue(content, key)
	if err != nil {
		return 0, err
	} else if len(resultBytes) < 3 ||
		resultBytes[0] != 'i' || resultBytes[len(resultBytes)-1] != 'e' {
		// Min size for integer is three ("i" + number + "e").
		return 0, fmt.Errorf("unable to parse integer with key \"%s\" "+
			"from torrent file", key)
	}

	// Remove staring "i" and ending "e" of bencoded integer before parsing.
	result, err := strconv.ParseInt(
		string(resultBytes[1:len(resultBytes)-1]),
		10,
		64,
	)
	if err != nil {
		return 0, err
	}

	return result, nil
}

// Gets the string stored in the bencoded dictionary with the key "key".
// Returns a NotFoundError if the key doesn't exist.
func GetString(content []byte, key string) (string, error) {
	resultBytes, err := GetDictValue(content, key)
	if err != nil {
		return "", err
	}

	start, end, err := getStringIndices(resultBytes)
	if err != nil {
		return "", err
	}

	return string(resultBytes[start:end]), nil
}

// Returns the index to the first byte of the "byte string" and
// the index of the byte just after the "given" bencoded string.
// Example, a call with these arguments:
//  content = 5:abcdei3e
//  index 	= 0
// would return 2, 7 and nil
func getStringIndices(content []byte) (stringStart int, stringEnd int, err error) {
	// Recover from index of out range (if the given content doesn't start with a string)
	defer func() {
		if r := recover(); r != nil {
			err = r.(error)
		}
	}()

	stringStart, stringEnd = 0, 0
	currentIndex := 0
	startIndex := 0
	for content[currentIndex] != ':' {
		currentIndex++
	}

	stringLength, err := strconv.Atoi(string(content[startIndex:currentIndex]))
	if err != nil {
		return stringStart, stringEnd, fmt.Errorf("unable to convert benocded "+
			"string length to integer: %w", err)
	}

	stringStart = currentIndex + 1
	stringEnd = stringStart + stringLength
	return stringStart, stringEnd, nil
}

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
func GetFiles(content []byte) ([]Files, error) {
	info, err := GetDictValue(content, "info")
	if err != nil {
		return nil, err
	}

	// If the "files" field exists: this is a multi file torrent.
	// Else: it is a single files torrent. (see comment on format over this function)
	multipleFiles := true
	filesInterface, err := GetDictInterface(info, "files")
	if err != nil {
		if _, ok := err.(*NotFoundError); ok {
			// The key "files" where not found, this is a single file torrent
			multipleFiles = false
		} else {
			return nil, err
		}
	}

	files := make([]Files, 0, 1)

	if multipleFiles {
		/*
			Multiple files torrent
		*/
		var totalLength int64 = 0

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

	} else {
		/*
			Single file torrent
		*/
		// TODO: uglier code while using interfaces and casting over and over again,
		//  but would be better performance compared to current code using bytes and
		//  looking through the bytes in linear time.
		name, err := GetString(info, "name")
		if err != nil {
			return nil, err
		}

		length, err := GetInt(info, "length")
		if err != nil {
			return nil, err
		}

		// Reuse the "name" field as "path"
		files = append(
			files,
			Files{
				Index:  0,
				Length: length,
				Path:   []string{name},
			},
		)
	}

	return files, nil
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
func getPeers(body []byte) (map[string]*peer.Peer, error) {
	// Get the bencoded value with the key "peers".
	// Is either a list or a string
	peersValue, err := GetDictValue(body, "peers")
	if err != nil {
		return nil, err
	}

	startIndex := 0
	currentIndex := 0
	peers := make(map[string]*peer.Peer)

	if peersValue[currentIndex] == 'l' {
		/*
			Dictionary model
		*/
		peersList, err := Decode(peersValue)
		if err != nil {
			return nil, err
		}

		peersListInterface, ok := peersList.([]interface{})
		if !ok {
			return nil, fmt.Errorf("unable to convert the \"peers\" value " +
				"into a list using the dictionary model")
		}

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

			// Use the peers "host:port" as key in the map.
			tmpPeer := peer.NewPeer(ipString, uint16(port))
			peers[tmpPeer.HostAndPort] = tmpPeer
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

			for i := 0; i < amountOfPeers; i++ {
				ip := net.IPv4(
					peersValue[currentIndex+(i*groupLen+0)],
					peersValue[currentIndex+(i*groupLen+1)],
					peersValue[currentIndex+(i*groupLen+2)],
					peersValue[currentIndex+(i*groupLen+3)],
				)

				portBytes := peersValue[currentIndex+(i*groupLen+4) : currentIndex+(i*groupLen+6)]
				port := binary.BigEndian.Uint16(portBytes)

				// Use the peers "host:port" as key in the map.
				tmpPeer := peer.NewPeer(ip.String(), port)
				peers[tmpPeer.HostAndPort] = tmpPeer
			}

		} else {
			return nil, fmt.Errorf("incorrect format of the field \"peers\" "+
				"from a binary model tracker, expected: string, got: %q", rune(peersValue[currentIndex]))
		}
	}

	return peers, nil
}
