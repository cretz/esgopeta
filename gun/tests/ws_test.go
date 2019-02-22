package tests

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func (t *testContext) startGunWebSocketProxyLogger(listenPort, targetPort int) {
	fromGun, toGun := t.startGunWebSocketProxy(listenPort, targetPort)
	time.Sleep(time.Second)
	go func() {
		for {
			select {
			case msg, ok := <-fromGun:
				if !ok {
					return
				}
				if testing.Verbose() {
					t.debugf("From gun raw: %v", string(msg))
					for _, s := range t.formattedGunJSONs(msg) {
						t.debugf("From gun: %v", s)
					}
				}
			case msg, ok := <-toGun:
				if !ok {
					return
				}
				if testing.Verbose() {
					t.debugf("To gun raw: %v", string(msg))
					if len(msg) == 0 {
						t.debugf("To gun: empty message")
					} else {
						for _, s := range t.formattedGunJSONs(msg) {
							t.debugf("To gun: %v", s)
						}
					}
				}
			}
		}
	}()
}

func (t *testContext) formattedGunJSONs(msg []byte) []string {
	var objs []interface{}
	if msg[0] == '[' {
		arr := []string{}
		t.Require.NoError(json.Unmarshal(msg, &arr))
		for _, v := range arr {
			var obj interface{}
			t.Require.NoError(json.Unmarshal([]byte(v), &obj))
			objs = append(objs, obj)
		}
	} else {
		var obj interface{}
		t.Require.NoError(json.Unmarshal(msg, &obj))
		objs = append(objs, obj)
	}
	ret := make([]string, len(objs))
	for i, obj := range objs {
		b, err := json.MarshalIndent(obj, "", "  ")
		t.Require.NoError(err)
		ret[i] = string(b)
	}
	return ret
}

func (t *testContext) startGunWebSocketProxy(listenPort, targetPort int) (fromTarget <-chan []byte, toTarget <-chan []byte) {
	fromTargetCh := make(chan []byte)
	toTargetCh := make(chan []byte)
	server := &http.Server{
		Addr: "127.0.0.1:" + strconv.Itoa(listenPort),
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.debugf("New ws proxy connection")
			err := t.handleGunWebSocketProxy(targetPort, w, r, fromTargetCh, toTargetCh)
			if _, ok := err.(*websocket.CloseError); !ok {
				t.debugf("Unexpected web socket close error: %v", err)
			}
		}),
	}
	serverErrCh := make(chan error, 1)
	go func() { serverErrCh <- server.ListenAndServe() }()
	go func() {
		defer server.Close()
		select {
		case <-t.Done():
		case err := <-serverErrCh:
			log.Printf("Server error: %v", err)
		}
	}()
	return fromTargetCh, toTargetCh
}

var wsDefaultUpgrader = websocket.Upgrader{}

func (t *testContext) handleGunWebSocketProxy(
	targetPort int,
	w http.ResponseWriter,
	r *http.Request,
	fromOther chan<- []byte,
	toOther chan<- []byte,
) error {
	otherConn, _, err := websocket.DefaultDialer.DialContext(t, "ws://127.0.0.1:"+strconv.Itoa(targetPort)+"/gun", nil)
	if err != nil {
		return err
	}
	defer otherConn.Close()
	// Upgrade
	c, err := wsDefaultUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return err
	}
	defer c.Close()
	type readMsg struct {
		messageType int
		p           []byte
		err         error
	}

	readCh := make(chan *readMsg)
	go func() {
		for {
			msg := new(readMsg)
			msg.messageType, msg.p, msg.err = c.ReadMessage()
			readCh <- msg
		}
	}()
	otherReadCh := make(chan *readMsg)
	go func() {
		for {
			msg := new(readMsg)
			msg.messageType, msg.p, msg.err = otherConn.ReadMessage()
			otherReadCh <- msg
		}
	}()
	for {
		select {
		case msg := <-readCh:
			if msg.err != nil {
				return msg.err
			}
			toOther <- msg.p
			if err := otherConn.WriteMessage(msg.messageType, msg.p); err != nil {
				return err
			}
		case otherMsg := <-otherReadCh:
			if otherMsg.err != nil {
				return otherMsg.err
			}
			fromOther <- otherMsg.p
			if err := c.WriteMessage(otherMsg.messageType, otherMsg.p); err != nil {
				return err
			}
		case <-t.Done():
			return t.Err()
		}
	}
}
