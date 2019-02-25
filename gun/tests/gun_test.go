package tests

import (
	"strings"
	"testing"

	"github.com/cretz/esgopeta/gun"
)

func TestGunGetSimple(t *testing.T) {
	// Run the server, put in one call, get in another, then check
	ctx, cancelFn := newContextWithGunJServer(t)
	defer cancelFn()
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
	r := g.Scoped(ctx, "esgopeta-test", "TestGunGetSimple", "some-key").FetchOne(ctx)
	ctx.Require.NoError(r.Err)
	ctx.Require.Equal(gun.ValueString(randStr), r.Value.(gun.ValueString))
	// // Do it again TODO: make sure there are no network calls, it's all from mem
	// ctx.debugf("Asking for key again")
	// f = g.Scoped(ctx, "esgopeta-test", "TestGunGetSimple", "some-key").FetchOne(ctx)
	// ctx.Require.NoError(f.Err)
	// ctx.Require.Equal(gun.ValueString(randStr), f.Value.(gun.ValueString))
}

func TestGunPutSimple(t *testing.T) {
	ctx, cancelFn := newContextWithGunJServer(t)
	defer cancelFn()
	randStr := randString(30)
	// Put
	g := ctx.newGunConnectedToGunJS()
	defer g.Close()
	// Just wait for two acks (one local, one remote)
	ch := g.Scoped(ctx, "esgopeta-test", "TestGunPutSimple", "some-key").Put(ctx, gun.ValueString(randStr))
	// TODO: test local is null peer and remote is non-null
	r := <-ch
	ctx.Require.NoError(r.Err)
	r = <-ch
	ctx.Require.NoError(r.Err)
	// Get from JS
	out := ctx.runJSWithGun(`
		gun.get('esgopeta-test').get('TestGunPutSimple').get('some-key').once(data => {
			console.log(data)
			process.exit(0)
		})
	`)
	ctx.Require.Equal(randStr, strings.TrimSpace(string(out)))
}
