// Contains logic related to the handshake with remote peers.
package peer

import (
	"bytes"
	"crypto/sha1"
	"fmt"
	"net"
	"time"

	bt "github.com/jmatss/torc/internal/util/bittorrent"
	"github.com/jmatss/torc/internal/util/logger"
)

const (
	Protocol         = "tcp"
	HandshakeTimeout = 5 * time.Second
)

// TODO: make sure to do "os.IsTimeout" on the returned error to see if it as timeout
// https://wiki.theory.org/index.php/BitTorrentSpecification#Handshake
// Initiates a handshake with the peer.
func (p *Peer) Handshake(infoHash [sha1.Size]byte, peerId string) (net.Conn, error) {
	conn, err := net.Dial(Protocol, p.HostAndPort)
	if err != nil {
		return nil, fmt.Errorf("unable to establish connection to "+
			"%s: %w", p.HostAndPort, err)
	}

	p.Connection = conn
	defer func() {
		if err != nil {
			p.Connection.Close()
		}
	}()

	if err = p.Connection.SetDeadline(time.Now().Add(HandshakeTimeout)); err != nil {
		return nil, fmt.Errorf("unable to set deadline for connection to "+
			"%s: %w", p.Connection.RemoteAddr().String(), err)
	}

	if err = p.sendHandshake(infoHash, peerId); err != nil {
		return nil, err
	}

	if err = p.recvHandshake(infoHash); err != nil {
		return nil, err
	}

	if err = p.Connection.SetDeadline(time.Now().Add(ConnectionTimeout)); err != nil {
		return nil, fmt.Errorf("unable to set deadline for connection to "+
			"%s: %w", p.Connection.RemoteAddr().String(), err)
	}

	logger.Log(logger.High, "Peer handshake done")

	return conn, nil
}

/*
TODO: implement so that this client can receive handshake from a remote peer.
 Listen on socket.
func RecvHandshake(peerId string, infoHash [sha1.Size]byte) (Peer, error) {}
*/

func (p *Peer) sendHandshake(infoHash [sha1.Size]byte, peerId string) error {
	// handshake: <pstrlen><pstr><reserved><info_hash><peer_id>
	dataLength := 1 + len(bt.PStr) + 8 + len(infoHash) + len(peerId)
	data := make([]byte, 0, dataLength)

	data = append(data, byte(len(bt.PStr)))     // pstrlen
	data = append(data, bt.PStr...)             // pstr
	data = append(data, bt.Reserved...)         // reserved
	data = append(data, []byte(infoHash[:])...) // info_hash
	data = append(data, []byte(peerId)...)      // peer_id

	if len(data) != dataLength {
		return fmt.Errorf("data given to handshake incorrect size, "+
			"expected: %d, got: %d", dataLength, len(data))
	}

	n, err := p.Connection.Write(data)
	if err != nil {
		return fmt.Errorf("unable to send handshake message: %w", err)
	} else if n != len(data) {
		return fmt.Errorf("data sent during handshake incorrect size, "+
			"expected: %d, got: %d", len(data), n)
	}

	logger.Log(logger.High, "Sent peer handshake to %s", p.Connection.RemoteAddr())

	return nil
}

func (p *Peer) recvHandshake(infoHash [sha1.Size]byte) error {
	// The handshake message is 49+len(pstr) bytes.
	// len(pstr) is stored in the first byte.
	lenpstrByte := make([]byte, 1)
	n, err := p.Connection.Read(lenpstrByte)
	if err != nil || n != len(lenpstrByte) {
		return fmt.Errorf("unable to read the first byte of handshake message "+
			"sent from remote peer %s: %w", p.Connection.RemoteAddr(), err)
	}
	lenpstr := int(lenpstrByte[0])

	// Read rest of handshake response
	response := make([]byte, 49+lenpstr-1)
	n, err = p.Connection.Read(response)
	if err != nil {
		return fmt.Errorf("unable to read handshake message from remote peer "+
			"%s: %w", p.Connection.RemoteAddr(), err)
	} else if n != len(response) {
		return fmt.Errorf("unexpected amount of bytes read from remote peer %s"+
			" during handshake. expected: %d, got: %d", p.Connection.RemoteAddr(), len(response), n)
	}

	// Received handshake format: <pstrlen><pstr><reserved><info_hash><peer_id>
	// TODO: Ignoring pstr, reserved & peerid, only interested in infoHash,
	//  implement more functionality later.
	start := lenpstr + len(bt.Reserved)
	end := lenpstr + len(bt.Reserved) + len(infoHash)
	remoteInfoHash := response[start:end]

	if !bytes.Equal(remoteInfoHash, infoHash[:]) {
		return fmt.Errorf("received incorrect hash id from remote %s, "+
			"expected: %040x, got: %040x", p.Connection.RemoteAddr().String(), infoHash, remoteInfoHash)
	}

	logger.Log(logger.High, "Received peer handshake from %s", p.Connection.RemoteAddr())

	return nil
}
