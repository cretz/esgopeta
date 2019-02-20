package gun

// type Scoped interface {
// 	Path() []string
// 	// Shortcut for last Path() entry or empty string
// 	Key() string
// 	Scoped(...string) Scoped
// 	Up(count int) Scoped
// 	// Shortcut for Up(1)
// 	Parent() Scoped
// 	// Shortcut for Up(-1)
// 	Root() Scoped

// 	Val(context.Context) *ValueFetch
// 	Watch(context.Context) <-chan *ValueFetch
// 	WatchChildren(context.Context) <-chan *ValueFetch
// 	Put(context.Context, Value) <-chan *Ack
// 	Add(context.Context, Value) <-chan *Ack
// }

type Scoped struct {
	gun  *Gun
	path []string
}

type ValueFetch struct {
	Err   error
	Key   string
	Value Value
	Peer  Peer
}

type Value interface {
}

type Ack struct {
	Err  error
	Ok   bool
	Peer Peer
}
