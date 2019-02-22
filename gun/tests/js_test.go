package tests

import (
	"strings"
	"testing"
)

func TestSimpleJS(t *testing.T) {
	ctx, cancelFn := newContext(t)
	defer cancelFn()
	ctx.Require.Equal("yay 3\n", string(ctx.runJS("console.log('yay', 1 + 2)")))
}

func TestGunJS(t *testing.T) {
	// Run the server, put in one call, get in another, then check
	ctx, cancelFn := newContext(t)
	defer cancelFn()
	ctx.startGunJSServer()
	randStr := randString(30)
	ctx.runJSWithGun(`
		gun.get('esgopeta-test').get('TestGunJS').get('some-key').put('` + randStr + `', ack => {
			if (ack.err) {
				console.error(ack.err)
				process.exit(1)
			}
			process.exit(0)
		})
	`)
	out := ctx.runJSWithGun(`
		gun.get('esgopeta-test').get('TestGunJS').get('some-key').once(data => {
			console.log(data)
			process.exit(0)
		})
	`)
	ctx.Require.Equal(randStr, strings.TrimSpace(string(out)))
}
