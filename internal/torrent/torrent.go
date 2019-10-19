package torrent

import (
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackpal/bencode-go"
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
	Pieces      string // Will consist of multiple 20-byte sha1 Pieces, might be better to split into slices
	Name        string // Is ignored; "path" in "Files" is used instead.
	Files       []Files
}

type Files struct {
	// Index is the start index of this file in the whole "byte stream".
	Index  int64
	Length int64
	Path   []string
}

// Create and return a new Torrent struct including a Tracker struct.
func NewTorrent(filename string) (*Torrent, error) {
	filename = filepath.FromSlash(filename)

	// see if file exists
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
	} else if torrent.Info.PieceLength == 0 {
		return nil, fmt.Errorf("unable to parse piece length from torrent: %w", err)
	} else if torrent.Info.Pieces == "" || len(torrent.Info.Pieces)%sha1.Size != 0 {
		return nil, fmt.Errorf("unable to parse pieces from torrent: %w", err)
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

// Writes the data received in the "piece" message to files.
// Takes the data part of an "piece" message as input.
//
// Returns the amount of bytes written or an error.
func (t *Torrent) WriteData(pieceData []byte) (int, error) {
	pieceIndex := binary.BigEndian.Uint32(pieceData[:4])
	begin := binary.BigEndian.Uint32(pieceData[4:8])
	data := pieceData[8:]

	if pieceIndex < 0 || pieceIndex >= uint32(len(t.Info.Pieces)/sha1.Size) {
		return 0, fmt.Errorf("piece index is incorrect: "+
			"expected: %d >= pieceIndex >= 0, got: %d", len(t.Info.Pieces)/sha1.Size, pieceIndex)
	}
	if begin < 0 || begin >= uint32(t.Info.PieceLength) {
		return 0, fmt.Errorf("begin is incorrect: "+
			"expected: %d >= pieceIndex >= 0, got: %d", t.Info.PieceLength, begin)
	}
	if len(data) > int(MaxRequestLength) {
		return 0, fmt.Errorf("length is over MaxRequestLength: "+
			"expected: <%d, got: %d", MaxRequestLength, len(data))
	}

	t.Tracker.Lock()
	defer t.Tracker.Unlock()

	// The "real" index of the whole "byte stream".
	requestIndex := int64(pieceIndex)*t.Info.PieceLength + int64(begin)
	var off int64 = 0

	for _, file := range t.Info.Files {
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

	if pieceIndex <= 0 || pieceIndex >= uint32(len(t.Info.Pieces)/sha1.Size) {
		return nil, fmt.Errorf("piece index is incorrect: "+
			"expected: %d >= pieceIndex >= 0, got: %d", len(t.Info.Pieces)/sha1.Size, pieceIndex)
	}
	if begin <= 0 || begin >= uint32(t.Info.PieceLength) {
		return nil, fmt.Errorf("begin is incorrect: "+
			"expected: %d >= pieceIndex >= 0, got: %d", t.Info.PieceLength, begin)
	}
	if length > MaxRequestLength {
		return nil, fmt.Errorf("length is over MaxRequestLength: "+
			"expected: <%d, got: %d", MaxRequestLength, length)
	}

	// The "real" index of the whole "byte stream" where the remote peer wants
	// to start reading data at. The remote peer wants "length" bytes starting
	// from this index.
	requestIndex := int64(pieceIndex)*t.Info.PieceLength + int64(begin)
	data := make([]byte, length)
	dataBuffer := data

	for _, file := range t.Info.Files {
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
	realHash := t.Info.Pieces[pieceIndex*sha1.Size : pieceIndex*sha1.Size+sha1.Size]

	if string(receivedHash[:]) == realHash {
		return true
	} else {
		return false
	}
}
