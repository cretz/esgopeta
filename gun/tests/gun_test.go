package tests

/*
func TestGunGo(t *testing.T) {
	// Run the server, put in one call, get in another, then check
	ctx, cancelFn := newContext(t)
	defer cancelFn()
	ctx.startGunServer(8080)
	ctx.startGunWebSocketProxyLogger(8081, 8080)
	randStr := randString(30)
	ctx.runJS(`
		var Gun = require('gun')
		const gun = Gun({
			peers: ['http://127.0.0.1:8081/gun'],
			radisk: false
		})
		gun.get('esgopeta-test').get('TestGunJS').get('some-key').put('` + randStr + `', ack => {
			if (ack.err) {
				console.error(ack.err)
				process.exit(1)
			}
			process.exit(0)
		})
	`)
	// out := ctx.runJS(`
	// 	var Gun = require('gun')
	// 	const gun = Gun({
	// 		peers: ['http://127.0.0.1:8081/gun'],
	// 		radisk: false
	// 	})
	// 	gun.get('esgopeta-test').get('TestGunJS').get('some-key').once(data => {
	// 		console.log(data)
	// 		process.exit(0)
	// 	})
	// `)
	// ctx.Require.Equal(randStr, strings.TrimSpace(string(out)))

	g, err := gun.NewFromPeerURLs(ctx, "http://127.0.0.1:8081/gun")
	ctx.Require.NoError(err)
	f := g.Scoped("esgopeta-test", "TestGunJS", "some-key").Val(ctx)
	ctx.Require.NoError(f.Err)

}
*/
