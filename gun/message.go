package gun

type Message struct {
	Ack  string             `json:"@,omitEmpty"`
	ID   string             `json:"#,omitEmpty"`
	To   string             `json:"><,omitEmpty"`
	Hash string             `json:"##,omitempty"`
	How  string             `json:"how,omitempty"`
	Get  *MessageGetRequest `json:"get,omitempty"`
	Put  map[string]*Node   `json:"put,omitempty"`
	DAM  string             `json:"dam,omitempty"`
	PID  string             `json:"pid,omitempty"`
}

type MessageGetRequest struct {
	Soul  string `json:"#,omitempty"`
	Field string `json:".,omitempty"`
}

type MessageReceived struct {
	*Message
	Peer Peer
}
