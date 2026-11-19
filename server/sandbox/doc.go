// Package sandbox serves the sandbox API. A request is admitted against
// what the driver says it can enforce, and one that asks for more is
// refused, never run weaker. What is admitted is recorded and handed to
// the driver, which converges the provider to the record.
package sandbox
