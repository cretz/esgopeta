package tests

import (
	"strings"
	"testing"

	"github.com/cretz/esgopeta/gun"
)

func TestGunGetSimple(t *testing.T) {
	// Run the server, put in one call, get in another, then check
	ctx, cancelFn := newContext(t)
	defer cancelFn()
	serverCancelFn := ctx.startGunJSServer()
	defer serverCancelFn()
	randStr := randString(30)
	// Write w/ JS
	ctx.runJSWithGun(`
		gun.get('esgopeta-test').get('TestGunGetSimple').get('some-field').put('` + randStr + `', ack => {
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
	r := g.Scoped(ctx, "esgopeta-test", "TestGunGetSimple", "some-field").FetchOne(ctx)
	ctx.Require.NoError(r.Err)
	ctx.Require.Equal(gun.ValueString(randStr), r.Value.(gun.ValueString))
	// Do it again with the JS server closed since it should fetch from memory
	serverCancelFn()
	ctx.debugf("Asking for field again")
	r = g.Scoped(ctx, "esgopeta-test", "TestGunGetSimple", "some-field").FetchOne(ctx)
	ctx.Require.NoError(r.Err)
	ctx.Require.Equal(gun.ValueString(randStr), r.Value.(gun.ValueString))
}

func TestGunGetSimpleRemote(t *testing.T) {
	// Do the above but w/ remote server
	ctx, cancelFn := newContext(t)
	defer cancelFn()
	remoteURL := ctx.prepareRemoteGunServer(defaultRemoteGunServerURL)
	randField, randVal := "field-"+randString(30), gun.ValueString(randString(30))
	// Write w/ JS
	ctx.debugf("Writing value")
	ctx.runJSWithGunURL(remoteURL, `
		gun.get('esgopeta-test').get('TestGunGetSimpleRemote').get('`+randField+`').put('`+string(randVal)+`', ack => {
			if (ack.err) {
				console.error(ack.err)
				process.exit(1)
			}
			process.exit(0)
		})
	`)
	// Get
	ctx.debugf("Reading value")
	g := ctx.newGunConnectedToGunServer(remoteURL)
	defer g.Close()
	// Make sure we got back the same value
	r := g.Scoped(ctx, "esgopeta-test", "TestGunGetSimpleRemote", randField).FetchOne(ctx)
	ctx.Require.NoError(r.Err)
	ctx.Require.Equal(randVal, r.Value)
}

func TestGunPutSimple(t *testing.T) {
	ctx, cancelFn := newContextWithGunJServer(t)
	defer cancelFn()
	randStr := randString(30)
	// Put
	g := ctx.newGunConnectedToGunJS()
	defer g.Close()
	// Just wait for two acks (one local, one remote)
	ch := g.Scoped(ctx, "esgopeta-test", "TestGunPutSimple", "some-field").Put(ctx, gun.ValueString(randStr))
	// TODO: test local is null peer and remote is non-null
	r := <-ch
	ctx.Require.NoError(r.Err)
	r = <-ch
	ctx.Require.NoError(r.Err)
	// Get from JS
	out := ctx.runJSWithGun(`
		gun.get('esgopeta-test').get('TestGunPutSimple').get('some-field').once(data => {
			console.log(data)
			process.exit(0)
		})
	`)
	ctx.Require.Equal(randStr, strings.TrimSpace(string(out)))
}

func TestGunPubSubSimpleRemote(t *testing.T) {
	ctx, cancelFn := newContext(t)
	defer cancelFn()
	remoteURL := ctx.prepareRemoteGunServer(defaultRemoteGunServerURL)
	randField, randVal := "field-"+randString(30), gun.ValueString(randString(30))
	// Start a fetcher
	ctx.debugf("Starting fetcher")
	fetchGun := ctx.newGunConnectedToGunServer(remoteURL)
	defer fetchGun.Close()
	fetchCh := fetchGun.Scoped(ctx, "esgopeta-test", "TestGunPubSubSimpleRemote", randField).Fetch(ctx)
	// Now put it from another instance
	ctx.debugf("Putting data")
	putGun := ctx.newGunConnectedToGunServer(remoteURL)
	defer putGun.Close()
	putScope := putGun.Scoped(ctx, "esgopeta-test", "TestGunPubSubSimpleRemote", randField)
	putScope.Put(ctx, randVal)
	ctx.debugf("Checking fetcher")
	// See that the fetch got the value
	for {
		select {
		case <-ctx.Done():
			ctx.Require.NoError(ctx.Err())
		case result := <-fetchCh:
			ctx.Require.NoError(result.Err)
			if !result.ValueExists {
				ctx.debugf("No value, trying again (got %v)", result)
				continue
			}
			ctx.Require.Equal(randVal, result.Value)
			return
		}
	}
}

/*
TODO Tests to write:
* test put w/ future state happens then
* test put w/ old state is discarded
* test put w/ new state is persisted
* test put w/ same state but greater is persisted
* test put w/ same state but less is discarded
*/
