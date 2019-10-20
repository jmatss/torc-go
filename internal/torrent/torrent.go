package torrent

import (
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jmatss/torc/internal/util/cons"
	"github.com/jmatss/torc/internal/util/logger"
)

const (
	Port = 6881 // TODO: make list of ports instead(?) (6881-6889)

	// 2^15 max according to unofficial specs.
	// See https://wiki.theory.org/index.php/BitTorrentSpecification#Messages
	// (section "request")
	MaxRequestLength uint32 = 1 << 14
)

type PieceHash [sha1.Size]byte

// See https://wiki.theory.org/index.php/BitTorrentSpecification#Metainfo_File_Structure
// for contents of torrent file.
//
// Keep single and multiple file mode in a similar struct where the length of
// "Files" in single file mode is 1.
type Torrent struct {
	sync.RWMutex

	Announce string
	Tracker  Tracker

	// Name is the "root" directory if this torrent contains multiple files or
	// if this torrent contains a single file, Name will be equal Files.Path.
	Name  string
	Files []Files

	// Contains sha1 hashes corresponding to every piece.
	Pieces      []PieceHash
	PieceLength int64 `bencode:"Piece length"`
}

type Files struct {
	// Index is the start index of this file in the whole "byte stream" in bytes.
	//
	//  For example if a torrent contains two files with size 16 Byte.
	//  The first file will have Index = 0, and the second index = 16.
	Index  int64
	Length int64
	Path   []string
}

// Create and return a new Torrent struct including a Tracker struct.
func NewTorrent(filename string) (*Torrent, error) {
	filename = filepath.FromSlash(filename)

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

	content, err := ioutil.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("unable to read from torrent file %s: %w",
			file.Name(), err)
	}

	announce, err := GetString(content, "announce")
	if err != nil {
		return nil, err
	} else if announce == "" {
		return nil, fmt.Errorf("unable to parse \"announce\" from torrent: %w", err)
	}

	piecesString, err := GetString(content, "pieces")
	if err != nil {
		return nil, err
	} else if piecesString == "" || len(piecesString)%sha1.Size != 0 {
		return nil, fmt.Errorf("unable to parse \"pieces\" from torrent: %w", err)
	}

	// Turn the received pieces string into slices of PieceHash ([sha1.Size]byte).
	// Will be faster/easier to access them later on.
	// TODO: possible to bypass need for copy?
	amountOfPieces := len(piecesString) / sha1.Size
	pieces := make([]PieceHash, 0, amountOfPieces)
	piecesSlice := []byte(piecesString)
	for i := 0; i < amountOfPieces; i++ {
		copy(pieces[i][:], piecesSlice[i*sha1.Size:i*sha1.Size+sha1.Size])
	}

	pieceLength, err := GetInt(content, "piece length")
	if err != nil {
		return nil, err
	} else if pieceLength == 0 {
		return nil, fmt.Errorf("unable to parse \"piece length\" from torrent: %w", err)
	}

	files, err := GetFiles(content)
	if err != nil {
		return nil, err
	}

	t := &Torrent{
		Announce:    announce,
		Pieces:      pieces,
		PieceLength: pieceLength,
		Files:       files,
	}

	err = NewTracker(content, t)
	if err != nil {
		return nil, fmt.Errorf("unable to create tracker for torrent: %w", err)
	}

	return t, nil
}

// Writes the data received in the "PieceHash" message to files.
// Takes the data part of an "PieceHash" message as input.
//
// Returns the amount of bytes written or an error.
func (t *Torrent) WriteData(pieceData []byte) (int, error) {
	pieceIndex := binary.BigEndian.Uint32(pieceData[:4])
	begin := binary.BigEndian.Uint32(pieceData[4:8])
	data := pieceData[8:]

	if pieceIndex < 0 || pieceIndex >= uint32(len(t.Pieces)/sha1.Size) {
		return 0, fmt.Errorf("PieceHash index is incorrect: "+
			"expected: %d >= pieceIndex >= 0, got: %d", len(t.Pieces)/sha1.Size, pieceIndex)
	}
	if begin < 0 || begin >= uint32(t.PieceLength) {
		return 0, fmt.Errorf("begin is incorrect: "+
			"expected: %d >= pieceIndex >= 0, got: %d", t.PieceLength, begin)
	}
	if len(data) > int(MaxRequestLength) {
		return 0, fmt.Errorf("length is over MaxRequestLength: "+
			"expected: <%d, got: %d", MaxRequestLength, len(data))
	}

	t.Tracker.Lock()
	defer t.Tracker.Unlock()

	// The "real" index of the whole "byte stream".
	requestIndex := int64(pieceIndex)*t.PieceLength + int64(begin)
	var off int64 = 0

	for _, file := range t.Files {
		// Skip files that have an index less than the requested start index.
		if requestIndex < file.Index {
			continue
		}

		path := filepath.FromSlash(cons.DownloadPath + strings.Join(file.Path, "/"))
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0444)
		if err != nil {
			return 0, fmt.Errorf("unable to open file %s: %w", path, err)
		}

		seekPos := requestIndex - file.Index
		_, err = f.Seek(seekPos, 0)
		if err != nil {
			f.Close()
			return 0, fmt.Errorf("unable to seek in file %s: %w", path, err)
		}

		/*
			If true:
			  This write will not fill the whole file.
			  Limit the amount of bytes writen to not get a índex out of bounds
			  when accessing the data in the "data" buffer.
			Else:
			  There are more data to write than there are bytes left in this file.
			  Will continue writing the rest of the data in the next file.
		*/
		amountToWrite := file.Length - seekPos
		if amountToWrite > int64(len(data)) {
			amountToWrite = int64(len(data))
		}

		n, err := f.Write(data[off : off+amountToWrite])
		if err != nil {
			f.Close()
			return 0, fmt.Errorf("unable to write data to file %s: %w", path, err)
		}

		logger.Log(logger.High, "%d bytes written to file %s", n, f.Name())

		f.Close()

		off += int64(n)
		// All the data have been written to files, break
		if off >= int64(len(data)) {
			break
		}
	}

	t.Tracker.Downloaded += int64(len(data))
	t.Tracker.Left -= int64(len(data))

	return len(data), nil
}

// Reads data that has been requested in the "request" message from disk.
// Takes a "request" message as input.
//
// Returns the data or an error.
func (t *Torrent) ReadData(request []byte) ([]byte, error) {
	pieceIndex := binary.BigEndian.Uint32(request[:4])
	begin := binary.BigEndian.Uint32(request[4:8])
	length := binary.BigEndian.Uint32(request[8:])

	if pieceIndex <= 0 || pieceIndex >= uint32(len(t.Pieces)/sha1.Size) {
		return nil, fmt.Errorf("PieceHash index is incorrect: "+
			"expected: %d >= pieceIndex >= 0, got: %d", len(t.Pieces)/sha1.Size, pieceIndex)
	}
	if begin <= 0 || begin >= uint32(t.PieceLength) {
		return nil, fmt.Errorf("begin is incorrect: "+
			"expected: %d >= pieceIndex >= 0, got: %d", t.PieceLength, begin)
	}
	if length > MaxRequestLength {
		return nil, fmt.Errorf("length is over MaxRequestLength: "+
			"expected: <%d, got: %d", MaxRequestLength, length)
	}

	// The "real" index of the whole "byte stream" where the remote peer wants
	// to start reading data at. The remote peer wants "length" bytes starting
	// from this index.
	requestIndex := int64(pieceIndex)*t.PieceLength + int64(begin)
	data := make([]byte, length)
	dataBuffer := data

	for _, file := range t.Files {
		// Skip files that have an index less than the requested start index.
		if requestIndex < file.Index {
			continue
		}

		path := filepath.FromSlash(cons.DownloadPath + "/" + strings.Join(file.Path, "/"))
		f, err := os.OpenFile(path, os.O_RDONLY, 0444)
		if err != nil {
			return nil, fmt.Errorf("unable to open file %s: %w", path, err)
		}

		_, err = f.Seek(requestIndex-file.Index, 0)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("unable to seek in file %s: %w", path, err)
		}

		n, err := f.Read(dataBuffer)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("unable to read data from file %s: %w", path, err)
		}
		f.Close()

		/*
			If true:
			  This file didn't contain all the requested data.
			  Continue reading data from the next file.
			  Change "offset"/start for byte slice so that the read data from this current file
			  doesn't get overwritten.
			Else:
			  All data read; break and return data to be sent to remote peer.
		*/
		if n < int(length) {
			dataBuffer = dataBuffer[n:]
		} else {
			break
		}
	}

	return data, nil
}

// Verifies the data received from the remote peer is the data that was requested.
// Compares the sha1 hash of the received data to the sha1 given from the tracker.
func (t *Torrent) IsCorrectPiece(pieceData []byte) bool {
	pieceIndex := binary.BigEndian.Uint32(pieceData[:4])

	receivedHash := sha1.Sum(pieceData)
	realHash := t.Pieces[pieceIndex]

	if receivedHash == realHash {
		return true
	} else {
		return false
	}
}
