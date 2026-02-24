package dicx

import "context"

// Starter is implemented by components that need initialization after
// construction. [Container.Start] calls Start in dependency order.
type Starter interface {
	Start(context.Context) error
}

// Stopper is implemented by components that hold resources requiring
// graceful shutdown. [Container.Stop] calls Stop in reverse dependency order.
type Stopper interface {
	Stop(context.Context) error
}
