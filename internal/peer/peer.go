package peer

import (
	"encoding/binary"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jmatss/torc/internal/torrent"
	bt "github.com/jmatss/torc/internal/util/bittorrent"
	"github.com/jmatss/torc/internal/util/logger"
)

const (
	ConnectionTimeout = 2 * time.Minute
)

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
func (p *Peer) Send(messageId bt.MessageId, input ...uint32) error {
	var data []byte
	id := byte(messageId)

	switch messageId {
	case bt.KeepAlive:
		// Format: <lenPrefix=0000>
		data = []byte{0, 0, 0, 0}
	case bt.Choke, bt.UnChoke, bt.Interested, bt.NotInterested:
		// Format: <lenPrefix=0001><id=X>
		data = []byte{0, 0, 0, 1, id}
	case bt.Have:
		// Format: <lenPrefix=0005><id=4><piece index>
		if len(input) != 1 {
			return fmt.Errorf("unable to send \"%s\" message: "+
				"incorrect amount of arguments to send function, "+
				"expected: 1, got: %d", messageId.String(), len(input))
		}

		lenPrefix := 5
		data = make([]byte, 4+lenPrefix)

		copy(data, []byte{0, 0, 0, byte(lenPrefix), id})
		binary.BigEndian.PutUint32(data[5:], input[0])
	case bt.Request, bt.Cancel:
		// Format: 	<lenPrefix=000(13)><id=X><index(4B)><begin(4B)><length(4B)>
		if len(input) != 3 {
			return fmt.Errorf("unable to send \"%s\" message: "+
				"incorrect amount of arguments to send function, "+
				"expected : 3, got: %d", messageId.String(), len(input))
		}

		lenPrefix := 13
		data = make([]byte, 4+lenPrefix)

		copy(data, []byte{0, 0, 0, byte(lenPrefix), id})
		binary.BigEndian.PutUint32(data[5:9], input[0])  // index
		binary.BigEndian.PutUint32(data[9:13], input[1]) // begin
		binary.BigEndian.PutUint32(data[13:], input[2])  // length
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

	logger.Log(logger.High, "Sent to %s - len: %d, id: %s",
		p.Connection.RemoteAddr(), len(data), messageId.String())

	return nil
}

// Sends a message to this peer containing binary data.
// Packet format: <length prefix><message ID><payload>
//
// This function can send:
// Bitfield or Piece messages
func (p *Peer) SendData(messageId bt.MessageId, payload []byte) error {
	lenPrefix := 1 + len(payload)
	id := byte(messageId)
	data := make([]byte, 0, 4+lenPrefix)

	data = append(data, []byte{0, 0, 0, byte(lenPrefix), id}...)
	data = append(data, payload...)

	n, err := p.Connection.Write(data)
	if err != nil {
		return err
	} else if n != len(data) {
		return fmt.Errorf("unable to send \"%s\" message to remote host %s",
			messageId.String(), p.Connection.RemoteAddr().String())
	}

	logger.Log(logger.High, "Sent data to %s: - len: %d, id: %s",
		p.Connection.RemoteAddr(), len(data), messageId.String())

	return nil
}

// Received a message on the connection for this peer.
// Returns the MessageId, some extra data in []byte format if needed and an error.
//
// Packet format: <length prefix><message ID><payload>
// Where <length prefix> is 4 bytes, <message ID> is 1 byte and <payload> is variable length.
func (p *Peer) Recv() (bt.MessageId, []byte, error) {
	// Reset deadline
	if err := p.Connection.SetDeadline(time.Now().Add(ConnectionTimeout)); err != nil {
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
	messageId := bt.MessageId(int(header[4]))

	if dataLen > torrent.MaxRequestLength*2 { // arbitrary value
		return 0, nil, fmt.Errorf("peer sent to many bytes, expected: >=%d, got: %d",
			torrent.MaxRequestLength, dataLen)
	}

	logger.Log(logger.High, "Recv from %s - header: 0x%x, datalen: %d, id: %s",
		p.Connection.RemoteAddr().String(), header[0:5], dataLen, messageId.String())

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
