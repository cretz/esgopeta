package tests

import (
	"bytes"
	"context"
	"log"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

type testContext struct {
	context.Context
	*testing.T
	Require *require.Assertions
}

func newContext(t *testing.T) (*testContext, context.CancelFunc) {
	return withTestContext(context.Background(), t)
}

func withTestContext(ctx context.Context, t *testing.T) (*testContext, context.CancelFunc) {
	ctx, cancelFn := context.WithCancel(ctx)
	return &testContext{
		Context: ctx,
		T:       t,
		Require: require.New(t),
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

func (t *testContext) startGunServer(port int) {
	t.startJS(`
		var Gun = require('gun')
		const server = require('http').createServer().listen(` + strconv.Itoa(port) + `)
		const gun = Gun({web: server, file: 'radata-server'})
	`)
}
