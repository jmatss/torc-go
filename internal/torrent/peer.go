package torrent

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"github.com/jmatss/torc/internal/util/cons"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	Protocol         = "tcp"
	HandshakeTimeout = 5 * time.Second
	Timeout          = 2 * time.Minute
)

const (
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
	sync.RWMutex

	UsingIp     bool
	Ip          net.IP
	Hostname    string
	Port        int64
	HostAndPort string

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

		// ip.To4 will return nil if it isn't a valid IPv4 address,
		// assume it is a IPv6 address
		if ip.To4() == nil {
			ipString = "[" + ipString + "]"
		}
	}
	peer.HostAndPort = ipString + ":" + strconv.Itoa(int(port))

	return peer
}

// TODO: make sure to do "os.IsTimeout" on the returned error to see if it as timeout
// https://wiki.theory.org/index.php/BitTorrentSpecification#Handshake
// Initiates a handshake with the peer.
func (p *Peer) Handshake(peerId string, infoHash [sha1.Size]byte) (net.Conn, error) {
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

	conn, err := net.Dial(Protocol, p.HostAndPort)
	if err != nil {
		return nil, fmt.Errorf("unable to establish connection to "+
			"%s: %w", p.HostAndPort, err)
	}
	defer func() {
		if err != nil {
			conn.Close()
		}
	}()

	err = conn.SetDeadline(time.Now().Add(HandshakeTimeout))
	if err != nil {
		return nil, fmt.Errorf("unable to set deadline for connection to "+
			"%s: %w", conn.RemoteAddr().String(), err)
	}

	n, err := conn.Write(data)
	if err != nil {
		return nil, fmt.Errorf("unable to send handshake message: %w", err)
	} else if n != len(data) {
		return nil, fmt.Errorf("data sent during handshake incorrect size, "+
			"expected: %d, got: %d", len(data), n)
	}

	// The handshake message is 49+len(pstr) bytes.
	// len(pstr) is stored in the first byte.
	lenpstrByte := make([]byte, 1)
	n, err = conn.Read(lenpstrByte)
	if err != nil {
		return nil, fmt.Errorf("unable to read handshake message: %w", err)
	} else if n != len(lenpstrByte) {
		return nil, fmt.Errorf("unable to read the first byte from remote (len(pstr))")
	}
	lenpstr := int(lenpstrByte[0])

	// Read rest of handshake response
	response := make([]byte, 49+lenpstr-1)
	n, err = conn.Read(response)
	if err != nil {
		return nil, fmt.Errorf("unable to read handshake message: %w", err)
	} else if n != len(response) {
		return nil, fmt.Errorf("unexpected amount of bytes read from remote during "+
			"handshake. expected: %d, got: %d", len(response), n)
	}

	// Received handshake format: <pstrlen><pstr><reserved><info_hash><peer_id>
	// Ignoring pstr, reserved & peerid, only interested in infoHash
	start := lenpstr + len(Reserved)
	end := lenpstr + len(Reserved) + len(infoHash)
	remoteInfoHash := response[start:end]

	if !bytes.Equal(remoteInfoHash, infoHash[:]) {
		return nil, fmt.Errorf("received incorrect hash id from remote %s, "+
			"expected: %040x, got: %040x", conn.RemoteAddr().String(), infoHash, remoteInfoHash)
	}

	err = conn.SetDeadline(time.Now().Add(Timeout))
	if err != nil {
		return nil, fmt.Errorf("unable to set deadline for connection to "+
			"%s: %w", conn.RemoteAddr().String(), err)
	}

	if cons.Logging == cons.High {
		log.Printf("Peer handshake done")
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
		data = make([]byte, 4+lenPrefix)

		copy(data, []byte{0, 0, 0, byte(lenPrefix), id})
		binary.BigEndian.PutUint32(data[5:9], uint32(input[0]))  // index
		binary.BigEndian.PutUint32(data[9:13], uint32(input[1])) // begin
		binary.BigEndian.PutUint32(data[13:], uint32(input[2]))  // length
	default:
		return fmt.Errorf("unexpected message id \"%d\"", messageId)
	}

	if cons.Logging == cons.High {
		log.Printf("Sent to %s - len: %d, id: %s", p.Connection.RemoteAddr(), len(data), messageId.String())
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

	if cons.Logging == cons.High {
		log.Printf("Sent data to %s: - len: %d, id: %s", p.Connection.RemoteAddr(), len(data), messageId.String())
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

// Received a message on the connection for this peer.
// Returns the MessageId, some extra data in []byte format if needed and an error.
//
// Packet format: <length prefix><message ID><payload>
// Where <length prefix> is 4 bytes, <message ID> is 1 byte and <payload> is variable length.
func (p *Peer) Recv() (MessageId, []byte, error) {
	// Reset deadline
	err := p.Connection.SetDeadline(time.Now().Add(Timeout))
	if err != nil {
		return -1, nil, fmt.Errorf("unable to set deadline for connection to "+
			"%s: %w", p.Connection.RemoteAddr().String(), err)
	}

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

	if dataLen > MaxRequestLength*2 { // arbitrary value
		return 0, nil, fmt.Errorf("peer sent to many bytes, expected: >=%d, got: %d",
			MaxRequestLength, dataLen)
	}

	if cons.Logging == cons.High {
		log.Printf("Recv from %s - header: 0x%x, datalen: %d, id: %s",
			p.Connection.RemoteAddr().String(), header[0:5], dataLen, messageId.String())
	}

	data := make([]byte, dataLen)
	off := 0
	if dataLen > 0 {
		for off < int(dataLen) {
			n, err = p.Connection.Read(data[off:])
			if err != nil {
				return 0, nil, err
			}
			off += n
		}
	}

	return messageId, data, nil
}

// Compares the IP/hostname of the peer.
// Will return false if a host has changed from using a hostname, IPv4 or IPv6 to one of the other,
// i.e. dns.google and 8.8.8.8 might be the same host, but this function will return false.
func (p *Peer) Equal(other *Peer) bool {
	if p.UsingIp != other.UsingIp { // "xor"
		return false
	}

	if p.UsingIp {
		return p.Ip.Equal(other.Ip)
	} else {
		return strings.ToLower(p.Hostname) == strings.ToLower(other.Hostname)
	}
}
