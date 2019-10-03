package torrent

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	Protocol   = "tcp"
	Timeout    = 5 * time.Second
	BufferSize = 1 << 16

	// The KeepAlive message doesn't have an id,
	//  set to -1 so that it still can be distinguished.
	KeepAlive MessageId = iota - 1
	Choke
	UnChoke
	Interested
	NotInterested
	Have
	Bitfield
	Request
	_ // Piece, not used
	Cancel
)

var (
	PStr     = []byte("BitTorrent protocol")
	PStrLen  = len(PStr)
	Reserved = []byte{0, 0, 0, 0, 0, 0, 0, 0}
)

type MessageId int

func (id MessageId) String() string {
	// enum indexing starts at "-1", need to increment with 1.
	return []string{
		"KeepAlive",
		"Choke",
		"UnChoke",
		"Interested",
		"NotInterested",
		"Have",
		"Bitfield",
		"Request",
		"Piece",
		"Cancel",
	}[id+1]
}

type Peer struct {
	UsingIp  bool
	Ip       net.IP
	Hostname string
	Port     int64

	Connection     net.Conn
	RemoteBitField []byte

	AmChoking     bool
	AmIntrested   bool
	PeerChoking   bool
	PeerIntrested bool
}

// Parameter ipString can be either IPv4, IPv6 or a hostname.
func NewPeer(ipString string, port int64) Peer {
	peer := Peer{
		Port: port,

		AmChoking:     true,
		AmIntrested:   false,
		PeerChoking:   true,
		PeerIntrested: false,
	}

	// Can parse either IPv4 or IPv6.
	// If it isn't a valid IP address it returns nil.
	// Assumes a invalid IP address is a hostname.
	ip := net.ParseIP(ipString)
	if ip == nil {
		peer.UsingIp = false
		peer.Hostname = ipString
	} else {
		peer.UsingIp = true
		peer.Ip = ip
	}

	return peer
}

// TODO: make sure to do "os.IsTimeout" on the returned error to see if it as timeout
// https://wiki.theory.org/index.php/BitTorrentSpecification#Handshake
// Initiates a handshake with the peer.
func (p *Peer) Handshake(peerId string, infoHash [sha1.Size]byte) (net.Conn, error) {
	var remote string
	port := strconv.Itoa(int(p.Port))

	if p.UsingIp {
		ip := p.Ip.String()
		if strings.Contains(ip, ":") {
			// Assume IP containing colon is IPv6, need to wrap inside square brackets
			ip = "[" + ip + "]"
		}

		remote = ip + ":" + port
	} else {
		remote = p.Hostname + ":" + port
	}

	// handshake: <pstrlen><pstr><reserved><info_hash><peer_id>
	// See specification link at top of function for more info
	dataLength := 1 + PStrLen + 8 + len(infoHash) + len(peerId)
	data := make([]byte, 0, dataLength)

	data = append(data, byte(PStrLen))          // pstrlen
	data = append(data, PStr...)                // pstr
	data = append(data, Reserved...)            // reserved
	data = append(data, []byte(infoHash[:])...) // info_hash
	data = append(data, []byte(peerId)...)      // peer_id

	if len(data) != dataLength {
		return nil, fmt.Errorf("data given to handshake incorrect size, "+
			"expected: %d, got: %d", dataLength, len(data))
	}

	conn, err := net.Dial(Protocol, remote)
	defer func() {
		if conn != nil {
			conn.Close()
		}
	}()
	if err != nil {
		return nil, fmt.Errorf("unable to establish connection to "+
			"%s: %w", remote, err)
	}

	err = conn.SetDeadline(time.Now().Add(Timeout))
	if err != nil {
		return nil, fmt.Errorf("unable to set deadline for connection to "+
			"%s: %w", conn.RemoteAddr().String(), err)
	}

	n, err := conn.Write(data)
	if err != nil {
		return nil, err
	} else if n != len(data) {
		return nil, fmt.Errorf("data sent during handshake incorrect size, "+
			"expected: %d, got: %d", len(data), n)
	}

	response := make([]byte, BufferSize)
	n, err = conn.Read(response)
	if err != nil {
		return nil, err
	} else if minLength := 1 + 0 + 8 + len(infoHash) + len(peerId); n < minLength {
		return nil, fmt.Errorf("remote peer %s sent less than minimum bytes "+
			"during handshake, expected >%d, got: %d",
			conn.RemoteAddr().String(), minLength, n)
	}

	// handshake: <pstrlen><pstr><reserved><info_hash><peer_id>
	pstrlen := int(response[0])
	// ignore pstr and reserved
	start := 1 + pstrlen + len(Reserved)
	end := 1 + pstrlen + len(Reserved) + len(infoHash)
	remoteInfoHash := response[start:end]
	// ignore peerid

	if !bytes.Equal(remoteInfoHash, infoHash[:]) {
		return nil, fmt.Errorf("received incorrect hash id from remote %s, "+
			"expected: %040x, got: %040x", conn.RemoteAddr().String(), infoHash, remoteInfoHash)
	}

	return conn, nil
}

/*
	All protocols except "handshake" follows format: <length prefix><message ID><payload>
	Where <length prefix> is 4 bytes, <message ID> is 1 byte and <payload> is variable length.
	See https://wiki.theory.org/index.php/BitTorrentSpecification#Messages
*/

// Sends a message to this peer.
// Packet format: <length prefix><message ID><payload>
//
// This function can send:
// KeepAlive, Choke, UnChoke, Interested, NotInterested, Have,
// Request and Cancel messages.
//
// It can not send:
// Bitfield or Piece messages
//
// Format of variadic int "input":
// - KeepAlive, Choke, UnChoke, Interested, NotInterested: Not used
// - Have: (<piece index>)
// - Request, Cancel: (<index>, <begin>, <length>)
func (p *Peer) Send(messageId MessageId, input ...int) error {
	var data []byte
	id := byte(messageId)

	switch messageId {
	case KeepAlive:
		// Format: <lenPrefix=0000>
		data = []byte{0, 0, 0, 0}
	case Choke, UnChoke, Interested, NotInterested:
		// Format: <lenPrefix=0001><id=X>
		data = []byte{0, 0, 0, 1, id}
	case Have:
		// Format: <lenPrefix=0005><id=4><piece index>
		if len(input) != 1 {
			return fmt.Errorf("unable to send \"%s\" message: "+
				"incorrect amount of arguments to send function, "+
				"expected: 1, got: %d", messageId.String(), len(input))
		}

		lenPrefix := 5
		data = make([]byte, 4+lenPrefix)

		copy(data, []byte{0, 0, 0, byte(lenPrefix), id})
		binary.BigEndian.PutUint32(data[5:], uint32(input[0]))
	case Request, Cancel:
		// Format: 	<lenPrefix=000(13)><id=X><index(4B)><begin(4B)><length(4B)>
		if len(input) != 3 {
			return fmt.Errorf("unable to send \"%s\" message: "+
				"incorrect amount of arguments to send function, "+
				"expected : 3, got: %d", messageId.String(), len(input))
		}

		lenPrefix := 13
		data := make([]byte, 4+lenPrefix)

		copy(data, []byte{0, 0, 0, byte(lenPrefix), id})
		binary.BigEndian.PutUint32(data[5:9], uint32(input[0]))  // index
		binary.BigEndian.PutUint32(data[9:13], uint32(input[1])) // begin
		binary.BigEndian.PutUint32(data[13:], uint32(input[2]))  // length
	default:
		return fmt.Errorf("unexpected message id \"%d\"", messageId)
	}

	n, err := p.Connection.Write(data)
	if err != nil {
		return err
	} else if n != len(data) {
		return fmt.Errorf("unable to send \"%s\" message to remote host %s",
			messageId.String(), p.Connection.RemoteAddr().String())
	}

	return nil
}

// Sends a bitfield message to the remote host.
// Optionally sent to remote host after handshake to indicate
// which pieces this client has. No need to send if this client
// doesn't have any pieces.
// Format: <lenPrefix=0001+len(bitfield)><id=5><bitfield>
func (p *Peer) SendBitfield(bitField []byte) error {
	lenPrefix := 1 + len(bitField)
	id := byte(5)
	data := make([]byte, 0, 4+lenPrefix)

	data = append(data, []byte{0, 0, 0, byte(lenPrefix), id}...)
	data = append(data, bitField...)

	n, err := p.Connection.Write(data)
	if err != nil {
		return err
	} else if n != len(data) {
		return fmt.Errorf("unable to send %s message to remote %s",
			Bitfield.String(), p.Connection.RemoteAddr().String())
	}

	return nil
}
