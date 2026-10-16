package platform

import "errors"

// ErrForeignPeer is returned for a connection from another user.
var ErrForeignPeer = errors.New("platform: connection from another user")
