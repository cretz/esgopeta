package gun

import "encoding/json"

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

type MessageGetRequest struct {
	Soul  string `json:"#,omitempty"`
	Field string `json:".,omitempty"`
}

type MessageReceived struct {
	*Message
	Peer *Peer
}
