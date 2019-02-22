package tests

import (
	"testing"
)

func TestGunGetSimple(t *testing.T) {
	// Run the server, put in one call, get in another, then check
	ctx, cancelFn := newContext(t)
	defer cancelFn()
	ctx.startGunJSServer()
	randStr := randString(30)
	// Write w/ JS
	ctx.runJSWithGun(`
		gun.get('esgopeta-test').get('TestGunGetSimple').get('some-key').put('` + randStr + `', ack => {
			if (ack.err) {
				console.error(ack.err)
				process.exit(1)
			}
			process.exit(0)
		})
	`)
	// Get
	g := ctx.newGunConnectedToGunJS()
	f := g.Scoped(ctx, "esgopeta-test", "TestGunGet", "some-key").Val(ctx)
	ctx.Require.NoError(f.Err)

}
