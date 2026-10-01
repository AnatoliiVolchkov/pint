package vmtest

import "go.uber.org/goleak"

// LeakOptions returns goleak options for integration tests.
//
// Idle keep-alive connections are owned by HTTP transports and closed by the runtime,
// they are not goroutines leaked by pint, so persistConn loops are ignored. Any other
// goroutine still running after a test, like a query worker, is reported.
func LeakOptions() []goleak.Option {
	return []goleak.Option{
		goleak.IgnoreAnyFunction("net/http.(*persistConn).readLoop"),
		goleak.IgnoreAnyFunction("net/http.(*persistConn).writeLoop"),
	}
}
