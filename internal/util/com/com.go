// ComMessages are sent between go processes over channels to communicate with each other.
// A Channel wraps channels so that messages can easily be sent in both directions.
package com

import (
	"fmt"
	"github.com/jmatss/torc/internal/torrent"
)

const (
	ChanSize = 10 // arbitrary value
)

type Id int

const (
	// Sent from "client" down towards "handlers"
	Add Id = iota
	Remove
	Start // Start stopped/paused torrent download
	Stop  // Stop/pause download
	Quit
)

const (
	// Sent from "handlers" up towards "client", starts at index 20
	Success Id = iota + 20 // general error
	Failure
	TotalFailure
	Exiting
	Complete
)

func (id Id) String() string {
	if id < 20 {
		return []string{
			"Add",
			"Remove",
			"Start",
			"Stop",
			"Quit",
		}[id]
	} else {
		return []string{
			"Success",
			"Failure",
			"TotalFailure",
			"Exiting",
			"Complete",
		}[id%20]
	}
}

type Message struct {
	Id      Id
	Torrent *torrent.Torrent

	// Set when sending a response. Error == nil: everything fine.
	Error error

	// Specified the child identifier if a child sent this msg.
	// Can be ex. a InfoHash
	Child string
}

// "Parent" is the channel that the children sends to and the parent receives on.
// "children" is a map of channels for every child that the parent can send to.
type Channel struct {
	Parent   chan Message
	children map[string]chan Message
}

func New() Channel {
	return Channel{
		Parent:   make(chan Message, ChanSize),
		children: make(map[string]chan Message),
	}
}

// Send a message to the parent from this "child".
func (ch *Channel) SendParent(
	id Id,
	error error,
	torrent *torrent.Torrent,
	child string,
) {
	sendNew(ch.Parent, id, error, torrent, child)
}

// Sends a message to the specified child.
// Returns true if it sent away the message correctly and false if the child channel
// doesn't exist (i.e. the child isn't in the "children" map).
func (ch *Channel) SendChild(
	id Id,
	error error,
	torrent *torrent.Torrent,
	child string,
) bool {
	childCh, ok := ch.children[child]
	if !ok {
		return false
	}

	sendNew(childCh, id, error, torrent, child)
	return true
}

// Sends a message to all children.
func (ch *Channel) SendChildren(id Id) {
	for child, childCh := range ch.children {
		sendNew(childCh, id, nil, nil, child)
	}
}

// Same as SendParent but a copy of a Message is sent.
// Modifies the Child field inside the message.
func (ch *Channel) SendParentCopy(msg Message, child string) bool {
	_, ok := ch.children[child]
	if !ok {
		return false
	}

	msg.Child = child
	send(ch.Parent, msg)
	return true
}

// Same as SendChild but a copy of a Message is sent.
// Modifies the Child field inside the message.
func (ch *Channel) SendChildCopy(msg Message, child string) bool {
	childCh, ok := ch.children[child]
	if !ok {
		return false
	}

	msg.Child = child
	send(childCh, msg)
	return true
}

func send(ch chan Message, msg Message) {
	ch <- msg
}

func sendNew(
	ch chan Message,
	id Id,
	error error,
	torrent *torrent.Torrent,
	child string,
) {
	msg := Message{id, torrent, error, child}
	send(ch, msg)
}

func (ch *Channel) RecvParent() Message {
	return recv(ch.Parent)
}

func (ch *Channel) RecvChild(child string) (Message, error) {
	childCh, ok := ch.children[child]
	if !ok {
		return Message{}, fmt.Errorf("the specified Child doesn't exists in this channel")
	}
	return recv(childCh), nil
}

func recv(ch chan Message) Message {
	return <-ch
}

// See if the specified child has an open channel.
func (ch *Channel) Exists(child string) bool {
	_, ok := ch.children[child]
	return ok
}

func (ch *Channel) CountChildren() int {
	return len(ch.children)
}

// Adds a child channel to this Channel. The child will add itself so that
// it can receive messages from the parent.
func (ch *Channel) AddChild(child string) {
	ch.children["Child"] = make(chan Message, ChanSize)
}

// Removes the channel for this child. Should be called when this child
// stops running (for example in a defer for the child go process).
func (ch *Channel) RemoveChild(child string) {
	childCh, ok := ch.children[child]
	if ok {
		sendNew(ch.Parent, Exiting, nil, nil, child)
		close(childCh)
	}
	delete(ch.children, child)
}

// Returns the channel for this child.
// A nil channel is returned if the child doesn't exists.
// The nil channel always blocks, so it will never be matched in a Select-statement.
func (ch *Channel) GetChildChannel(child string) chan Message {
	childCh, ok := ch.children[child]
	if !ok {
		return nil
	}
	return childCh
}
