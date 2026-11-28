//go:build !unix

package pluginhost

import "github.com/tetratelabs/wazero/experimental"

func mapMemory(_, _ uint64) experimental.LinearMemory { return nil }
