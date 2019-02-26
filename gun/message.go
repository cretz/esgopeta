package gun

import "encoding/json"

// Message is the JSON-encodable message that Gun peers send to each other.
type Message struct {
	Ack  string             `json:"@,omitempty"`
	ID   string             `json:"#,omitempty"`
	To   string             `json:"><,omitempty"`
	Hash json.Number        `json:"##,omitempty"`
	How  string             `json:"how,omitempty"`
	Get  *MessageGetRequest `json:"get,omitempty"`
	Put  map[string]*Node   `json:"put,omitempty"`
	DAM  string             `json:"dam,omitempty"`
	PID  string             `json:"pid,omitempty"`
	OK   int                `json:"ok,omitempty"`
	Err  string             `json:"err,omitempty"`
}

// MessageGetRequest is the format for Message.Get.
type MessageGetRequest struct {
	Soul  string `json:"#,omitempty"`
	Field string `json:".,omitempty"`
}

type messageReceived struct {
	*Message
	peer *Peer
	// storedPuts are the souls and their fields that have been stored by
	// another part of the code. This is useful if the main instance stores
	// something it sees, there's no need for the message listener to do so as
	// well.
	storedPuts map[string][]string
}
