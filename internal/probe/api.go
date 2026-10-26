package probe

import "errors"

// ErrNotImplemented is returned by stubs until P1-W1/P1-W2 land. The stub
// files stubs_w1.go and stubs_w2.go hold the contract signatures; each worker
// deletes its stub file and implements the same signatures as real functions.
var ErrNotImplemented = errors.New("probe: not implemented")
