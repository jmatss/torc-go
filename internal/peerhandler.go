package internal

import (
	"encoding/binary"
	"fmt"

	"github.com/jmatss/torc/internal/torrent"
	"github.com/jmatss/torc/internal/util/com"
	"github.com/jmatss/torc/internal/util/cons"
	"github.com/jmatss/torc/internal/util/logger"
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
	conn, err := peer.Handshake(cons.PeerId, tor.Tracker.InfoHash)
	if err != nil {
		logger.Log(logger.High, "PeerHandler handshake failure!: %w", err)
		comTorrentHandler.SendParent(com.TotalFailure, nil, err, nil, childId)
		return
	}
	peer.Connection = conn
	defer func() {
		peer.Connection.Close()
		logger.Log(logger.High, "PeerHandler exiting")
	}()

	comTorrentHandler.SendParent(com.Success, nil, nil, nil, childId)
	comTorrentHandler.AddChild(childId)
	defer comTorrentHandler.RemoveChild(childId)

	//peer.SendData(torrent.Bitfield, tor.Tracker.BitFieldHave)
	// Start by un choking remote peer and send that this client is interested
	// TODO: these state doesn't currently change after this point, CODE
	peer.Send(torrent.Interested)
	peer.Send(torrent.UnChoke)

	// RemoteBitField initialized to all zeros
	bitFieldLength := ((len(tor.Info.Pieces) / 20) / 8) + 1
	peer.RemoteBitField = make([]byte, bitFieldLength)

	/*
		Spawn a downloader that requests data from the remote peer.
		This PeerHandler will receive the data from the remote peer
		and forward it to the downloader via the downloadChannel.
	*/
	downloadChannel := make(chan remoteDTO, com.ChanSize)
	go downloader(comTorrentHandler, downloadChannel, tor, peer)

	/*
		Spawn a go process that receives data from the remote peer
		and puts it into the "readChannel".
	*/
	// TODO: modify buffer size
	// TODO: kill this go process when the PeerHandler exits
	readChannel := make(chan remoteDTO, com.ChanSize)
	go func() {
		for {
			id, data, err := peer.Recv()
			readChannel <- remoteDTO{id, data, err}

			// Kills itself if it receives a error.
			// The receiver of the message over the "readChannel" can check and see that
			// "Err" is set, and figure out that this go process is dead.
			if err != nil {
				logger.Log(logger.High, "read func exiting, err: %v", err)
				return
			}
		}
	}()

	for {
		select {
		case received := <-comTorrentHandler.GetChildChannel(childId):
			/*
				Received message from "torrentHandler"/parent.
			*/
			switch received.Id {
			case com.Have:
				pieceIndex := binary.BigEndian.Uint32(received.Data)
				peer.Send(torrent.Have, int(pieceIndex))

			case com.Quit:
				// TODO: Kill internal "readChannel" go process & downloader
				return
			}

		case received := <-readChannel:
			/*
				Received message from remote peer.
			*/
			// Kills itself if it receives an error
			if received.Err != nil {
				comTorrentHandler.SendParent(com.TotalFailure, nil, err, nil, childId)

				// TODO: need to kill downloader, do this in a better way
				if received.Id == torrent.Piece {
					downloadChannel <- received
				}
				return
			}

			switch received.Id {
			case torrent.KeepAlive:
				// TODO: do something?
			case torrent.Choke:
				peer.PeerChoking = true
				downloadChannel <- received
			case torrent.UnChoke:
				peer.PeerChoking = false
				downloadChannel <- received
			case torrent.Interested:
				peer.PeerInterested = true
				downloadChannel <- received
			case torrent.NotInterested:
				peer.PeerInterested = false
				downloadChannel <- received
			case torrent.Have:
				// Remote peer indicates that it has just received the piece with the index "pieceIndex".
				// Update the "RemoteBitField" in this peer struct by OR:ing in a 1 at the correct index.
				pieceIndex := binary.BigEndian.Uint32(received.Data)
				byteShift := pieceIndex / 8
				bitShift := 8 - (pieceIndex % 8) // bits are stored in "reverse"
				if int(byteShift) > len(peer.RemoteBitField) {
					comTorrentHandler.SendParent(
						com.TotalFailure,
						nil,
						fmt.Errorf("the remote peer has specified a piece index that is to big to "+
							"fit in it's bitfield"),
						nil,
						childId,
					)
					return
				}

				// TODO: might deadlock if the operations fails
				peer.Lock()
				peer.RemoteBitField[byteShift] |= 1 << bitShift
				peer.Unlock()

			case torrent.Bitfield:
				// If correct length, assume correct bitfield. Update local to match remote.
				if len(received.Data) == len(peer.RemoteBitField) {
					peer.Lock()
					peer.RemoteBitField = received.Data
					peer.Unlock()
				}

			case torrent.Request:
				// TODO: do in another go process or another file/function
				requestedData, err := tor.ReadData(received.Data)
				if err != nil {
					// TODO: some sort of logging or feedback of this failure.
					//  Not so important since it most likely the remote peer that has an error.
					break
				}

				// Send requested data to remote peer
				if err := peer.SendData(torrent.Piece, requestedData); err != nil {
					// TODO: some sort of logging or feedback of this failure.
				}

			case torrent.Piece:
				downloadChannel <- received

				// TODO: some sort of logging or feedback of this success.
			case torrent.Cancel:
				// TODO: Nothing to do atm, might need to add functionality later
			default:
				comTorrentHandler.SendParent(
					com.TotalFailure,
					nil,
					fmt.Errorf("unexpected message id \"%d\"", received.Id),
					nil,
					childId,
				)
				return
			}
		}
	}
}

// Download pieces from this remote peer.
func downloader(
	comTorrentHandler com.Channel,
	downloadChannel chan remoteDTO,
	t *torrent.Torrent,
	p *torrent.Peer,
) {
	for {
		pieceIndex, err := downloadPiece(downloadChannel, t, p)
		if err != nil {
			return
		}

		logger.Log(logger.High, "piece %d downloaded", pieceIndex)

		// Send have message to torrentHandler to let it now that a new piece is downloaded
		// and a Have message can be sent to all peers.
		data := make([]byte, 4)
		binary.BigEndian.PutUint32(data, uint32(pieceIndex))
		comTorrentHandler.SendParent(com.Have, data, nil, nil, "")
	}
}

func downloadPiece(downloadChannel chan remoteDTO, t *torrent.Torrent, p *torrent.Peer) (int, error) {
	pieceIndex, err := findFreePieceIndex(t, p)
	if err != nil {
		return -1, err
	}

	// The last piece will have a size less than t.Info.PieceLength, prevent overflow.
	// If true:
	//  this piece is the last piece, will have a size of less than "piece length"
	pieceLength := t.Info.PieceLength
	amountOfPieces := len(t.Info.Pieces) / 20
	if pieceIndex == amountOfPieces-1 {
		pieceLength = t.Tracker.Left
	}

	// Update BitFields in Tracker according to how this download operation went.
	// If error:
	//  clear BitFieldDownloading so that this piece can be re-downloaded.
	// Else:
	//  piece downloaded, set BitFieldHave to indicate that client has downloaded the piece.
	defer func() {
		t.Tracker.Lock()
		defer t.Tracker.Unlock()

		byteIndex := pieceIndex / 8
		bitIndex := pieceIndex % 8
		if err != nil {
			t.Tracker.BitFieldDownloading[byteIndex] &^= 1 << (7 - uint(bitIndex))
		} else {
			t.Tracker.BitFieldHave[byteIndex] |= 1 << (7 - uint(bitIndex))
		}
	}()

	// Read the whole piece one request at a time and write to disk.
	// TODO: More error checking. length of received data etc.
	// TODO: Add timeout so it doesn't hang if it doesn't get an answer.
	begin := 0
	var received remoteDTO
	for int64(begin) < pieceLength {
		// Prevent overflow of the last "block".
		// TODO: fix possibility for overflow (in64 -> int)
		requestLength := torrent.MaxRequestLength
		if int64(begin+torrent.MaxRequestLength) > pieceLength {
			requestLength = int(pieceLength) - begin
		}

		for {
			// Loop forever until the remote peer un chokes this client and this client
			// receives a "piece" message.
			// TODO: will never continue from here if this client doesn't receive a
			//  unchoke. Implement timeout.
			if p.PeerChoking {
				received = <-downloadChannel
				if received.Err != nil {
					return -1, received.Err
				}
			} else {
				p.Send(torrent.Request, pieceIndex, begin, requestLength)
				received = <-downloadChannel
				if received.Err != nil {
					return -1, received.Err
				} else if received.Id == torrent.Piece {
					break
				}
			}
		}

		if !t.IsCorrectPiece(received.Data) {
			logger.Log(logger.Low, "received incorrect piece from remote peer")
			return 0, fmt.Errorf("the received piece's sha1 hash is incorrect")
		}
		_, err := t.WriteData(received.Data)
		if err != nil {
			logger.Log(logger.Low, "error writing to file: %v", err)
			return 0, fmt.Errorf("unable to write to file: %v", err)
		}

		begin += requestLength
	}

	return pieceIndex, nil
}

// Returns a free piece that needs to be downloaded and this remote peer has.
// (or an error)
func findFreePieceIndex(t *torrent.Torrent, p *torrent.Peer) (int, error) {
	t.Tracker.Lock()
	defer t.Tracker.Unlock()
	p.Lock()
	defer p.Unlock()

	amountOfPieces := len(t.Info.Pieces) / 20

	for i := 0; i < amountOfPieces; i++ {
		byteIndex := i / 8
		bitIndex := i % 8
		taken := int(t.Tracker.BitFieldDownloading[byteIndex]) & (1 << (7 - uint(bitIndex)))

		// If taken < 1:
		//  this piece is free to download.
		//  If (this remote peer has that piece):
		//    download it
		//  Else:
		//    continue to loop through and look for a free piece that this remote peer has.
		// Else (taken >= 1):
		//  this piece is taken i.e. is downloaded or is being downloaded from other peer.
		//  continue to loop through and look for a free piece that this remote peer has.
		if taken < 1 {
			// Set the piece to "taken"
			t.Tracker.BitFieldDownloading[byteIndex] |= 1 << (7 - uint(bitIndex))
			return i, nil
		}
	}

	return -1, fmt.Errorf("unable to find a free piece for this peer")
}
