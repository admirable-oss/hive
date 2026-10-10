//go:build !linux

package platform

import "context"

func watchShutdown(context.Context, func(context.Context)) error { return ErrUnsupported }
