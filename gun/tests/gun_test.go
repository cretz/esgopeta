package tests

import (
	"testing"

	"github.com/cretz/esgopeta/gun"
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
	defer g.Close()
	// Make sure we got back the same value
	f := g.Scoped(ctx, "esgopeta-test", "TestGunGetSimple", "some-key").Val(ctx)
	ctx.Require.NoError(f.Err)
	ctx.Require.Equal(gun.ValueString(randStr), f.Value.(gun.ValueString))
	// // Do it again TODO: make sure there are no network calls, it's all from mem
	// ctx.debugf("Asking for key again")
	// f = g.Scoped(ctx, "esgopeta-test", "TestGunGetSimple", "some-key").Val(ctx)
	// ctx.Require.NoError(f.Err)
	// ctx.Require.Equal(gun.ValueString(randStr), f.Value.Value.(gun.ValueString))

}
