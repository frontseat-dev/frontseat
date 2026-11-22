// Package sandbox serves the sandbox API. A request is admitted against
// what the driver says it can enforce, and one that asks for more is
// refused, never run weaker. What is admitted is recorded and handed to
// the driver, which converges the provider to the record.
//
// Gateway returns the service over an OpenShell gateway for whatever is
// built on it and holds it in its own process (Service): the control
// plane, and frontseat-enterprise. GatewayRunner serves the same over a
// socket, for the runner of tests with services, and Handler serves it
// over any driver. The service keeps a record of each sandbox it made in
// a directory of its own, the state it is given, written before the
// sandbox is made and read strictly when it starts again; a spec asked
// with or answered is a copy, never the service's own.
package sandbox
