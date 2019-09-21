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
)

var (
	PStr     = []byte("BitTorrent protocol")
	PStrLen  = len(PStr)
	Reserved = []byte{0, 0, 0, 0, 0, 0, 0, 0}
)

type Peer struct {
	UsingIp  bool // bool to indicate if this peer is using Ip or Hostname
	Ip       net.IP
	Hostname string
	Port     int64

	AmChoking     bool // this client is 	choking remote peer
	AmIntrested   bool // 		 -||-		interested in remote peer
	PeerChoking   bool // remote peer is	choking this client
	PeerIntrested bool //		 -||-		interested in this client
}

func NewPeerIp(ip net.IP, port int64) Peer {
	return newPeer(true, ip, "", port)
}

func NewPeerHostname(hostname string, port int64) Peer {
	return newPeer(false, nil, hostname, port)
}

func newPeer(usingIp bool, ip net.IP, hostname string, port int64) Peer {
	peer := Peer{
		UsingIp: usingIp,
		Port:    port,

		AmChoking:     true,
		AmIntrested:   false,
		PeerChoking:   true,
		PeerIntrested: false,
	}

	if usingIp {
		peer.Ip = ip
	} else {
		peer.Hostname = hostname
	}

	return peer
}

func (p *Peer) PeerHandler() {
	// TODO: will be in charge of all communication between this client and the peer
}

// TODO: make sure to do "os.IsTimeout" on the returned error to see if it as timeout
// https://wiki.theory.org/index.php/BitTorrentSpecification#Handshake
// Initiates a handshake with the peer.
// ! The callee is in charge of closing the returned connection, even when
//  a error is returned !
func (p *Peer) Handshake(peerId string, infoHash [sha1.Size]byte) (net.Conn, error) {
	// Remote will be in the form:
	// "<IPv4/HOSTNAME>:<PORT>" or "[<IPv6>]:<PORT>"
	var remote string
	port := strconv.Itoa(int(p.Port))

	if p.UsingIp {
		ip := p.Ip.String()
		if strings.Contains(ip, ":") {
			// IPv6, need to wrap ip inside square brackets
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
	All other protocols follow format(excluding "handshake"): <length prefix><message ID><payload>
	Where <length prefix> is 4 bytes, <message ID> is 1 byte and <payload> is variable length.
	See https://wiki.theory.org/index.php/BitTorrentSpecification#Messages
*/

// Sends a keep alive message to make sure that the other party knows that this
// client is still alive and running.
// Format: <lenPrefix=0000>
func (p *Peer) KeepAlive(conn net.Conn) error {
	// TODO: send every ~2 min
	data := []byte{0, 0, 0, 0}
	n, err := conn.Write(data)
	if err != nil {
		return err
	} else if n != len(data) {
		return fmt.Errorf("unable to send keep alive message to remote %s",
			conn.RemoteAddr().String())
	}

	return nil
}

// TODO: might be able to merge Choke and Unchoke into one function
//  and see if it needs to be choked/unchoked in the boolean already
//  contained in the Peer struct. Same thing with interested/not interested.
// Sends a choke message to the remote host.
// Format: <lenPrefix=0001><id=0>
func (p *Peer) Choke(conn net.Conn) error {
	lenPrefix := 1
	id := byte(0)
	data := []byte{0, 0, 0, byte(lenPrefix), id}

	n, err := conn.Write(data)
	if err != nil {
		return err
	} else if n != len(data) {
		return fmt.Errorf("unable to send choke message to remote %s",
			conn.RemoteAddr().String())
	}

	return nil
}

// Sends a unchoke message to the remote host.
// Format: <lenPrefix=0001><id=1>
func (p *Peer) UnChoke(conn net.Conn) error {
	lenPrefix := 1
	id := byte(1)
	data := []byte{0, 0, 0, byte(lenPrefix), id}

	n, err := conn.Write(data)
	if err != nil {
		return err
	} else if n != len(data) {
		return fmt.Errorf("unable to send unchoke message to remote %s",
			conn.RemoteAddr().String())
	}

	return nil
}

// Sends a interested message to the remote host.
// Format: <lenPrefix=0001><id=2>
func (p *Peer) Interested(conn net.Conn) error {
	lenPrefix := 1
	id := byte(2)
	data := []byte{0, 0, 0, byte(lenPrefix), id}

	n, err := conn.Write(data)
	if err != nil {
		return err
	} else if n != len(data) {
		return fmt.Errorf("unable to send interested message to remote %s",
			conn.RemoteAddr().String())
	}

	return nil
}

// Sends a not interested message to the remote host.
// Format: <lenPrefix=0001><id=3>
func (p *Peer) NotInterested(conn net.Conn) error {
	lenPrefix := 1
	id := byte(3)
	data := []byte{0, 0, 0, byte(lenPrefix), id}

	n, err := conn.Write(data)
	if err != nil {
		return err
	} else if n != len(data) {
		return fmt.Errorf("unable to send not interested message to remote %s",
			conn.RemoteAddr().String())
	}

	return nil
}

// Sends a have message to the remote host.
// Sent to indicate that this client have acquired a new piece
// and it is available to download for other peers.
// Format: <lenPrefix=0005><id=4><piece index>
func (p *Peer) Have(conn net.Conn, pieceIndex int) error {
	lenPrefix := 5
	id := byte(4)
	data := make([]byte, 4+lenPrefix)

	copy(data, []byte{0, 0, 0, byte(lenPrefix), id})
	binary.BigEndian.PutUint32(data[5:], uint32(pieceIndex))

	n, err := conn.Write(data)
	if err != nil {
		return err
	} else if n != len(data) {
		return fmt.Errorf("unable to send have message to remote %s",
			conn.RemoteAddr().String())
	}

	return nil
}

// Sends a bitfield message to the remote host.
// Optionally sent to remote host after handshake to indicate
// which pieces this client has.
// Format: <lenPrefix=0001+len(bitfield)><id=5><bitfield>
func (p *Peer) Bitfield(conn net.Conn, bitField []byte) error {
	lenPrefix := 1 + len(bitField)
	id := byte(5)
	data := make([]byte, 0, 4+lenPrefix)

	data = append(data, []byte{0, 0, 0, byte(lenPrefix), id}...)
	data = append(data, bitField...)

	n, err := conn.Write(data)
	if err != nil {
		return err
	} else if n != len(data) {
		return fmt.Errorf("unable to send bitfield message to remote %s",
			conn.RemoteAddr().String())
	}

	return nil
}

// Sends a request message to the remote host.
// Used to request specific pieces from the remote host.
// Format: <lenPrefix=0013><id=6><index(4B)><begin(4B)><length(4B)>
func (p *Peer) Request(conn net.Conn, index, begin, length int) error {
	lenPrefix := 13
	id := byte(6)
	data := make([]byte, 4+lenPrefix)

	copy(data, []byte{0, 0, 0, byte(lenPrefix), id})
	binary.BigEndian.PutUint32(data[5:9], uint32(index))
	binary.BigEndian.PutUint32(data[9:13], uint32(begin))
	binary.BigEndian.PutUint32(data[13:], uint32(length))

	n, err := conn.Write(data)
	if err != nil {
		return err
	} else if n != len(data) {
		return fmt.Errorf("unable to send request message to remote %s",
			conn.RemoteAddr().String())
	}

	return nil
}

// Sends a cancel message to the remote host.
// Used to cancel requested blocks.
// Format: <lenPrefix=0013><id=8><index(4B)><begin(4B)><length(4B)>
func (p *Peer) CancelRequest(conn net.Conn, index, begin, length int) error {
	lenPrefix := 13
	id := byte(8)
	data := make([]byte, 4+lenPrefix)

	copy(data, []byte{0, 0, 0, byte(lenPrefix), id})
	binary.BigEndian.PutUint32(data[5:9], uint32(index))
	binary.BigEndian.PutUint32(data[9:13], uint32(begin))
	binary.BigEndian.PutUint32(data[13:], uint32(length))

	n, err := conn.Write(data)
	if err != nil {
		return err
	} else if n != len(data) {
		return fmt.Errorf("unable to send cancel request message to remote %s",
			conn.RemoteAddr().String())
	}

	return nil
}
