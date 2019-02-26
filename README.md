# Esgopeta [![GoDoc](https://godoc.org/github.com/cretz/esgopeta/gun?status.svg)](https://godoc.org/github.com/cretz/esgopeta/gun)

Esgopeta is a Go implementation of the [Gun](https://github.com/amark/gun) distributed graph database. See the
[Godoc](https://godoc.org/github.com/cretz/esgopeta/gun) for API details.

**WARNING: This is an early proof-of-concept alpha version. Many pieces are not implemented.**

Features:

* Client for reading and writing w/ rudimentary conflict resolution
* In-memory storage

Not yet implemented:

* Server
* Alternative storage methods
* SEA (i.e. encryption/auth)

### Usage

The package is `github.com/cretz/esgopeta/gun` which can be fetched via `go get`. To listen to database changes for a
value, use `Fetch`. The example below listens for updates on a key for a minute:

```go
package main

import (
	"context"
	"log"
	"time"

	"github.com/cretz/esgopeta/gun"
)

func main() {
	// Let's listen for a minute
	ctx, cancelFn := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancelFn()
	// Create the Gun client connecting to common Gun server
	g, err := gun.New(ctx, gun.Config{
		PeerURLs:         []string{"https://gunjs.herokuapp.com/gun"},
		PeerErrorHandler: func(err *gun.ErrPeer) { log.Print(err) },
	})
	if err != nil {
		log.Panic(err)
	}
	// Issue a fetch and get a channel for updates
	fetchCh := g.Scoped(ctx, "esgopeta-example", "sample-key").Fetch(ctx)
	// Log all updates and exit when context times out
	log.Print("Waiting for value")
	for {
		select {
		case <-ctx.Done():
			log.Print("Time's up")
			return
		case fetchResult := <-fetchCh:
			if fetchResult.Err != nil {
				log.Printf("Error fetching: %v", fetchResult.Err)
			} else if fetchResult.ValueExists {
				log.Printf("Got value: %v", fetchResult.Value)
			}
		}
	}
}
```

When that's running, we can send values via a `Put`. The example below sends two updates for that key:

```go
package main

import (
	"context"
	"log"
	"time"

	"github.com/cretz/esgopeta/gun"
)

func main() {
	// Give a 1 minute timeout, but shouldn't get hit
	ctx, cancelFn := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancelFn()
	// Create the Gun client connecting to common Gun server
	g, err := gun.New(ctx, gun.Config{
		PeerURLs:         []string{"https://gunjs.herokuapp.com/gun"},
		PeerErrorHandler: func(err *gun.ErrPeer) { log.Print(err) },
	})
	if err != nil {
		log.Panic(err)
	}
	// Issue a simple put and wait for a single peer ack
	putScope := g.Scoped(ctx, "esgopeta-example", "sample-key")
	log.Print("Sending first value")
	putCh := putScope.Put(ctx, gun.ValueString("first value"))
	for {
		if result := <-putCh; result.Err != nil {
			log.Printf("Error putting: %v", result.Err)
		} else if result.Peer != nil {
			log.Printf("Got ack from %v", result.Peer)
			break
		}
	}
	// Let's send another value
	log.Print("Sending second value")
	putCh = putScope.Put(ctx, gun.ValueString("second value"))
	for {
		if result := <-putCh; result.Err != nil {
			log.Printf("Error putting: %v", result.Err)
		} else if result.Peer != nil {
			log.Printf("Got ack from %v", result.Peer)
			break
		}
	}
}
```

Note, these are just examples and you may want to control the lifetime of the channels better. See the
[Godoc](https://godoc.org/github.com/cretz/esgopeta/gun) for more information.