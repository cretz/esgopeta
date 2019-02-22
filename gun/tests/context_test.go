package tests

import (
	"bytes"
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/cretz/esgopeta/gun"
	"github.com/stretchr/testify/require"
)

type testContext struct {
	context.Context
	*testing.T
	Require   *require.Assertions
	GunJSPort int
}

func newContext(t *testing.T) (*testContext, context.CancelFunc) {
	return withTestContext(context.Background(), t)
}

const defaultGunJSPort = 8080

func withTestContext(ctx context.Context, t *testing.T) (*testContext, context.CancelFunc) {
	ctx, cancelFn := context.WithCancel(ctx)
	return &testContext{
		Context:   ctx,
		T:         t,
		Require:   require.New(t),
		GunJSPort: defaultGunJSPort,
	}, cancelFn
}

func (t *testContext) debugf(format string, args ...interface{}) {
	if testing.Verbose() {
		log.Printf(format, args...)
	}
}

func (t *testContext) runJS(script string) []byte {
	cmd := exec.CommandContext(t, "node")
	_, currFile, _, _ := runtime.Caller(0)
	cmd.Dir = filepath.Dir(currFile)
	cmd.Stdin = bytes.NewReader([]byte(script))
	out, err := cmd.CombinedOutput()
	out = removeGunJSWelcome(out)
	t.Require.NoErrorf(err, "JS failure, output:\n%v", string(out))
	return out
}

func (t *testContext) runJSWithGun(script string) []byte {
	return t.runJS(`
		var Gun = require('gun')
		const gun = Gun({
			peers: ['http://127.0.0.1:` + strconv.Itoa(t.GunJSPort) + `/gun'],
			radisk: false
		})
		` + script)
}

func (t *testContext) startJS(script string) (*bytes.Buffer, *exec.Cmd, context.CancelFunc) {
	cmdCtx, cancelFn := context.WithCancel(t)
	cmd := exec.CommandContext(cmdCtx, "node")
	_, currFile, _, _ := runtime.Caller(0)
	cmd.Dir = filepath.Dir(currFile)
	cmd.Stdin = bytes.NewReader([]byte(script))
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	t.Require.NoError(cmd.Start())
	return &buf, cmd, cancelFn
}

func (t *testContext) startGunJSServer() {
	// If we're logging, use a proxy
	port := t.GunJSPort
	if testing.Verbose() {
		t.startGunWebSocketProxyLogger(port, port+1)
		port++
	}
	// Remove entire data folder first
	t.Require.NoError(os.RemoveAll("radata-server"))
	t.startJS(`
		var Gun = require('gun')
		const server = require('http').createServer().listen(` + strconv.Itoa(port) + `)
		const gun = Gun({web: server, file: 'radata-server'})
	`)
}

func (t *testContext) newGunConnectedToGunJS() *gun.Gun {
	config := gun.Config{
		PeerURLs: []string{"http://127.0.0.1:" + strconv.Itoa(t.GunJSPort) + "/gun"},
		PeerErrorHandler: func(errPeer *gun.ErrPeer) {
			t.debugf("Got peer error: %v", errPeer)
		},
	}
	g, err := gun.New(t, config)
	t.Require.NoError(err)
	return g
}
