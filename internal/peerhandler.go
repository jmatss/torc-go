package internal

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jmatss/torc/internal/torrent"
	"github.com/jmatss/torc/internal/util/com"
)

const (
	// 2^15 max according to unofficial specs.
	// See https://wiki.theory.org/index.php/BitTorrentSpecification#Messages
	// (section "request")
	MaxRequestLength = 1 << 15
)

type remoteDTO struct {
	Id   torrent.MessageId
	Data []byte
	Err  error
}

// Handler in charge of one specific peer.
// TODO: dont send torrent argument like this, ugly
func PeerHandler(comTorrentHandler com.Channel, peer *torrent.Peer, tor *torrent.Torrent) {
	childId := string(peer.HostAndPort)

	// Peer handshake. This handler will kill itself if it isn't able to
	// complete the handshake.
	conn, err := peer.Handshake(PeerId, tor.Tracker.InfoHash)
	if err != nil {
		comTorrentHandler.SendParent(com.TotalFailure, err, nil, childId)
		return
	}
	peer.Connection = conn
	defer peer.Connection.Close()
	comTorrentHandler.SendParent(com.Success, nil, nil, childId)
	comTorrentHandler.AddChild(childId)
	defer comTorrentHandler.RemoveChild(childId)

	// RemoteBitField initialized to all zeros
	bitFieldLength := ((len(tor.Info.Pieces) / 20) / 8) + 1
	peer.RemoteBitField = make([]byte, bitFieldLength)

	// TODO: modify buffer size
	// TODO: kill this go process when the PeerHandler exits
	readChannel := make(chan remoteDTO, com.ChanSize)
	go func() {
		for true {
			id, data, err := peer.Recv()
			readChannel <- remoteDTO{id, data, err}

			// Kills itself if it receives a error.
			// The receiver of the message over the "readChannel" can check and see that
			// "Err" is set, and figure out that this go process is dead.
			if err != nil {
				return
			}
		}
	}()

	select {
	case received := <-comTorrentHandler.GetChildChannel(childId):
		/*
			Received message from "torrentHandler"/parent.
		*/
		switch received.Id {
		case com.Quit:
			// TODO: Kill internal "readChannel" go process
			return
		}

	case received := <-readChannel:
		/*
			Received message from remote peer.
		*/
		// Kills itself if it receives and error
		if received.Err != nil {
			comTorrentHandler.SendParent(com.TotalFailure, err, nil, childId)
			return
		}

		switch received.Id {
		case torrent.KeepAlive:
			// TODO: do something?
		case torrent.Choke:
			peer.PeerChoking = true
		case torrent.UnChoke:
			peer.PeerChoking = false
		case torrent.Interested:
			peer.PeerInterested = true
		case torrent.NotInterested:
			peer.PeerInterested = false
		case torrent.Have:
			// Remote peer indicates that it has just received the piece with the index "pieceIndex".
			// Update the "RemoteBitField" in this peer struct by OR:ing in a 1 at the correct index.
			pieceIndex := binary.BigEndian.Uint32(received.Data)
			byteShift := pieceIndex / 8
			bitShift := 8 - (pieceIndex % 8) // bits are stored in "reverse"
			if int(byteShift) > len(peer.RemoteBitField) {
				comTorrentHandler.SendParent(
					com.TotalFailure,
					fmt.Errorf("the remote peer has specified a piece index that is to big to "+
						"fit in it's bitfield"),
					nil,
					childId,
				)
				return
			}
			peer.RemoteBitField[byteShift] |= 1 << bitShift

		case torrent.Bitfield:
			// If correct length, assume correct bitfield. Update local to match remote.
			if len(received.Data) == len(peer.RemoteBitField) {
				peer.RemoteBitField = received.Data
			}

		case torrent.Request:
			// TODO: do in another go process or another file/function
			requestedData, err := getData(tor, received.Data)
			if err != nil {
				// TODO: some sort of logging or feedback of this failure.
				//  Not so important since it most likely the remote peer that has an error.
				break
			}

			// Send request data to remote peer
			if err := peer.SendData(torrent.Piece, requestedData); err != nil {
				// TODO: some sort of logging or feedback of this failure.
			}

		case torrent.Piece:
			if err := setData(tor, received.Data); err != nil {
				comTorrentHandler.SendParent(
					com.Failure,
					err,
					tor,
					childId,
				)
			}

			// TODO: some sort of logging or feedback of this success.
		case torrent.Cancel:
			// TODO: Nothing to do atm, might need to add functionality later
		default:
			comTorrentHandler.SendParent(
				com.TotalFailure,
				fmt.Errorf("unexpected message id \"%d\"", received.Id),
				nil,
				childId,
			)
			return
		}
	}
}

// TODO: add more argument validation so that it doesn't request data outside the piece etc.
func getData(tor *torrent.Torrent, request []byte) ([]byte, error) {
	pieceIndex := binary.BigEndian.Uint32(request[:4])
	begin := binary.BigEndian.Uint32(request[4:8])
	length := binary.BigEndian.Uint32(request[8:])

	if pieceIndex <= 0 || pieceIndex >= uint32(len(tor.Info.Pieces)/20) {
		return nil, fmt.Errorf("piece index is incorrect: "+
			"expected: %d >= pieceIndex >= 0, got: %d", len(tor.Info.Pieces)/20, pieceIndex)
	}
	if begin <= 0 || begin >= uint32(tor.Info.PieceLength) {
		return nil, fmt.Errorf("begin is incorrect: "+
			"expected: %d >= pieceIndex >= 0, got: %d", tor.Info.PieceLength, begin)
	}
	if length > MaxRequestLength {
		return nil, fmt.Errorf("length is over MaxRequestLength: "+
			"expected: <%d, got: %d", MaxRequestLength, length)
	}

	// The "real" index of the whole "byte stream" where the remote peer wants
	// to start reading data at. The remote peer wants "length" bytes starting
	// from this index.
	requestIndex := int64(pieceIndex)*tor.Info.PieceLength + int64(begin)
	data := make([]byte, length)
	dataBuffer := data

	for _, file := range tor.Info.Files {
		// Skip files that have an index less than the requested start index.
		if requestIndex < file.Index {
			continue
		}

		path := filepath.FromSlash(DownloadPath + "/" + strings.Join(file.Path, "/"))
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
			  Change "offset"/start for byte slice so that the read data from this file
			  doesn't get overwritten.
			Else:
			  All data read, break and send data to remote peer.
		*/
		if n < int(length) {
			dataBuffer = dataBuffer[n:]
		} else {
			break
		}
	}

	return data, nil
}

func setData(tor *torrent.Torrent, piece []byte) error {
	pieceIndex := binary.BigEndian.Uint32(piece[:4])
	begin := binary.BigEndian.Uint32(piece[4:8])
	data := piece[8:]

	if pieceIndex <= 0 || pieceIndex >= uint32(len(tor.Info.Pieces)/20) {
		return fmt.Errorf("piece index is incorrect: "+
			"expected: %d >= pieceIndex >= 0, got: %d", len(tor.Info.Pieces)/20, pieceIndex)
	}
	if begin <= 0 || begin >= uint32(tor.Info.PieceLength) {
		return fmt.Errorf("begin is incorrect: "+
			"expected: %d >= pieceIndex >= 0, got: %d", tor.Info.PieceLength, begin)
	}
	if len(data) > MaxRequestLength {
		return fmt.Errorf("length is over MaxRequestLength: "+
			"expected: <%d, got: %d", MaxRequestLength, len(data))
	}

	// The "real" index of the whole "byte stream".
	requestIndex := int64(pieceIndex)*tor.Info.PieceLength + int64(begin)
	var off int64 = 0

	for _, file := range tor.Info.Files {
		// Skip files that have an index less than the requested start index.
		if requestIndex < file.Index {
			continue
		}

		path := filepath.FromSlash(DownloadPath + "/" + strings.Join(file.Path, "/"))
		f, err := os.OpenFile(path, os.O_WRONLY, 0444)
		if err != nil {
			return fmt.Errorf("unable to open file %s: %w", path, err)
		}

		seekPos := requestIndex - file.Index
		_, err = f.Seek(seekPos, 0)
		if err != nil {
			f.Close()
			return fmt.Errorf("unable to seek in file %s: %w", path, err)
		}

		/*
			If true:
			  This write will not fill the whole file.
			  Limit the amount of bytes writen to not get a índex out of bounds
			  when accessing the data in the "data" buffer.
			Else:
			  There are more data to write then there are bytes left in this file.
			  Will continue writing the rest of the data in the next file.
		*/
		amountToWrite := file.Length - seekPos
		if amountToWrite > int64(len(data)) {
			amountToWrite = int64(len(data))
		}

		n, err := f.Write(data[off : off+amountToWrite])
		if err != nil {
			f.Close()
			return fmt.Errorf("unable to write data to file %s: %w", path, err)
		}
		f.Close()

		off += int64(n)
		// All the data have been written to files, break
		if off >= int64(len(data)) {
			break
		}
	}

	return nil
}
