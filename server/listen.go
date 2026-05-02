package server

import "net"

// net_Listen wraps net.Listen so the server package keeps a single
// import path for the standard library — convenient when swapping in
// a fake listener in tests via dependency injection.
func net_Listen(network, addr string) (net.Listener, error) {
	return net.Listen(network, addr)
}
