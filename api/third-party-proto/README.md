# third-party-proto

Protos the public API imports and does not own, copied in at a pinned
version so a build never reaches the Buf Schema Registry: of a grid's
actions only a fetch has a network, and only to the proxy. Never
edited here; excluded from our lint and breaking rules.

| File | From | Version | License |
|---|---|---|---|
| `google/api/field_behavior.proto` | `buf.build/googleapis/googleapis` | commit `8ac61b9e502343289c8c42b17aa5b245` | Apache-2.0 (header) |
| `google/rpc/error_details.proto` | `buf.build/googleapis/googleapis` | commit `8ac61b9e502343289c8c42b17aa5b245` | Apache-2.0 (header) |
| `google/rpc/status.proto`, `google/rpc/code.proto` | `buf.build/googleapis/googleapis` | commit `8ac61b9e502343289c8c42b17aa5b245` | Apache-2.0 (header) |

To update one: `buf export <module>:<version> -o <dir>`, copy the file over,
and change the version here. Their Go code is not generated here: it is
`google.golang.org/genproto/googleapis/{api/annotations,rpc/errdetails}`,
the packages each file's `go_package` names.
