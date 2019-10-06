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
	Protocol            = "tcp"
	Timeout             = 5 * time.Second
	HandshakeBufferSize = 1 << 7 // arbitrary size

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
	Piece
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

	AmChoking      bool
	AmInterested   bool
	PeerChoking    bool
	PeerInterested bool
}

// Parameter ipString can be either IPv4, IPv6 or a hostname.
func NewPeer(ipString string, port int64) Peer {
	peer := Peer{
		Port: port,

		AmChoking:      true,
		AmInterested:   false,
		PeerChoking:    true,
		PeerInterested: false,
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

	response := make([]byte, HandshakeBufferSize)
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
// - Have: <piece index>[0]
// - Request, Cancel: <index>[0], <begin>[1], <length>[2]
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

// Sends a message to this peer containing binary data.
// Packet format: <length prefix><message ID><payload>
//
// This function can send:
// Bitfield or Piece messages
func (p *Peer) SendData(messageId MessageId, payload []byte) error {
	lenPrefix := 1 + len(payload)
	id := byte(messageId)
	data := make([]byte, 0, 4+lenPrefix)

	data = append(data, []byte{0, 0, 0, byte(lenPrefix), id}...)
	data = append(data, payload...)

	n, err := p.Connection.Write(data)
	if err != nil {
		return err
	} else if n != len(data) {
		return fmt.Errorf("unable to send %s message to remote %s",
			Bitfield.String(), p.Connection.RemoteAddr().String())
	}

	return nil
}

// Received a message on the connection for this peer.
// Returns the MessageId, some extra data in []byte format if needed and an error.
//
// Packet format: <length prefix><message ID><payload>
// Where <length prefix> is 4 bytes, <message ID> is 1 byte and <payload> is variable length.
func (p *Peer) Recv() (MessageId, []byte, error) {
	// Read the "header" i.e. the first five bytes containing payload length and MessageId
	header := make([]byte, 5)
	n, err := p.Connection.Read(header)
	if err != nil {
		return 0, nil, err
	} else if n == 4 { // Assume this is a keep alive message
		// TODO: Keep a timer for keep alive messages so that this peer can be killed
		//  if it stops sending messages for a while. ~2 min seems to be a common time.
		return -1, nil, nil
	} else if n < 4 {
		return 0, nil, fmt.Errorf("peer sent to few bytes, expected: >=4, got: %d", n)
	}
	dataLen := binary.BigEndian.Uint32(header[:4]) - 1 // -1 to "remove" len of messageId
	messageId := MessageId(int(header[4]))

	var data []byte
	if dataLen > 0 {
		data = make([]byte, dataLen)
		n, err = p.Connection.Read(data)
		if err != nil {
			return 0, nil, err
		} else if n != int(dataLen) {
			return 0, nil, fmt.Errorf("incorrect amount of data (excl. header) received from "+
				"remote peer %s, expected: %d, got: %d", p.Connection.RemoteAddr().String(), dataLen, n)
		}
	}

	switch messageId {
	case KeepAlive:
		// TODO: Nothing to do atm, might need to add functionality later
		// This case will never be true
	case Choke:
		p.PeerChoking = true
	case UnChoke:
		p.PeerChoking = false
	case Interested:
		p.PeerInterested = true
	case NotInterested:
		p.PeerInterested = false
	case Have:
		// Remote peer indicates that it has just received the piece with the index "pieceIndex".
		// Update the "RemoteBitField" in this peer struct by OR:ing in a 1 at the correct index.
		pieceIndex := binary.BigEndian.Uint32(data)
		byteShift := pieceIndex / 8
		bitShift := 8 - (pieceIndex % 8) // bits are stored in "reverse"
		if int(byteShift) > len(p.RemoteBitField) {
			return 0, nil, fmt.Errorf("the remote peer has specified a piece index " +
				"that is to big to fit in it's bitfield")
		}
		p.RemoteBitField[byteShift] |= 1 << bitShift
	case Request:
		// TODO: Nothing to do atm, might need to add functionality later
	case Piece:
		// TODO: Nothing to do atm, might need to add functionality later
	case Cancel:
		// TODO: Nothing to do atm, might need to add functionality later
	default:
		return 0, nil, fmt.Errorf("unexpected message id \"%d\"", messageId)
	}

	return messageId, data, nil
}
