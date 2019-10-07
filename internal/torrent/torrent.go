package torrent

import (
	"fmt"
	"os"

	"github.com/jackpal/bencode-go"
)

const (
	Port = 6881 // TODO: make list of ports instead(?) (6881-6889)
)

// See https://wiki.theory.org/index.php/BitTorrentSpecification#Metainfo_File_Structure
// for contents of torrent file. This torrent struct only contains required fields.
type Torrent struct {
	Announce string
	Info     Info
	Tracker  Tracker
}

// Keep single and multiple file mode in a similar struct where the length of
// "Files" in single file mode is 1
type Info struct {
	PieceLength int64  `bencode:"piece length"`
	Pieces      string // will consist of multiple 20-byte sha1 Pieces, might be better to split into slices
	Name        string
	Files       []Files
}

type Files struct {
	Length int64
	Path   []string
}

// Create and return a new Torrent struct including a Tracker struct
func NewTorrent(filename string) (*Torrent, error) {
	// see if file exists and is a file (rather than dir)
	fileStat, err := os.Stat(filename)
	if err != nil {
		return nil, fmt.Errorf("unable to get stat of %s: %w", filename, err)
	} else if !fileStat.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", filename)
	}

	file, err := os.OpenFile(filename, os.O_RDONLY, 0444)
	if err != nil {
		return nil, fmt.Errorf("unable to open file %s: %w", filename, err)
	}
	defer file.Close()

	torrent := &Torrent{}
	err = bencode.Unmarshal(file, torrent)
	if err != nil {
		return nil, fmt.Errorf("unable to create new torrent: %w", err)
	}

	// Get data from the "files" field from the torrent file.
	file.Seek(0, 0)
	files, err := getFiles(file)
	if err != nil {
		return nil, err
	}
	torrent.Info.Files = files

	file.Seek(0, 0)
	err = NewTracker(torrent, file)
	if err != nil {
		return nil, fmt.Errorf("unable to create tracker for torrent: %w", err)
	}

	return torrent, nil
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
					files = append(files, Files{length, []string{name}})
				}
			}
		}
		return nil, fmt.Errorf("bencoded torrent file incorrect format: " +
			"unable to parse the \"length\" and/or \"name\" field")
	} else {
		/*
			Multiple files torrent
		*/
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

			files = append(files, Files{length, path})
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
