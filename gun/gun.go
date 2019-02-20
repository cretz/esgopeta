package gun

import (
	"context"
	"fmt"
	"net/url"
)

type Gun struct {
	peers   []Peer
	storage Storage
	soulGen func() Soul
}

type Config struct {
	Peers   []Peer
	Storage Storage
	SoulGen func() Soul
}

func New(config Config) *Gun {
	g := &Gun{
		peers:   make([]Peer, len(config.Peers)),
		storage: config.Storage,
		soulGen: config.SoulGen,
	}
	// Copy over peers
	copy(g.peers, config.Peers)
	// Set defaults
	if g.storage == nil {
		g.storage = &StorageInMem{}
	}
	if g.soulGen == nil {
		g.soulGen = SoulGenDefault
	}
	return g
}

// To note: Fails on even one peer failure (otherwise, do this yourself). May connect to
// some peers temporarily until first failure, but closes them all on failure
func NewFromPeerURLs(ctx context.Context, peerURLs ...string) (g *Gun, err error) {
	c := Config{Peers: make([]Peer, len(peerURLs))}
	for i := 0; i < len(peerURLs) && err == nil; i++ {
		if parsedURL, err := url.Parse(peerURLs[i]); err != nil {
			err = fmt.Errorf("Failed parsing peer URL %v: %v", peerURLs[i], err)
		} else if peerNew := PeerURLSchemes[parsedURL.Scheme]; peerNew == nil {
			err = fmt.Errorf("Unknown peer URL scheme for %v", peerURLs[i])
		} else if c.Peers[i], err = peerNew(ctx, parsedURL); err != nil {
			err = fmt.Errorf("Failed connecting to peer %v: %v", peerURLs[i], err)
		}
	}
	if err != nil {
		for _, peer := range c.Peers {
			peer.Close()
		}
		return
	}
	return New(c), nil
}

type Message struct {
	Ack    string `json:"@,omitEmpty"`
	ID     string `json:"#,omitEmpty"`
	Sender string `json:"><,omitEmpty"`
	Hash   string `json:"##,omitempty"`
	OK     *int   `json:"ok,omitempty"`
	How    string `json:"how,omitempty"`
	// TODO: "get", "put", "dam"
}
