# Container plugin

Starts a service for tests as a container, exactly as a catalog entity
writes it under `spec.frontseat.test-service`, with `type: container`: the
image, its arguments, ports, environment, readiness, its endpoints and
what a test is told. It is the plugin for whatever no other plugin knows
how to start, and adds nothing of its own; the host holds what was
written to what every service must say.

It finds no projects and declares no actions.
