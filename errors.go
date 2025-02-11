package mnemosyne

import (
	"errors"
)

var (
	ErrNilCache      = errors.New("nil object found in cache")
	ErrNilValue      = errors.New("cannot set nil value in cache")
	ErrLayerNotFound = errors.New("cache layer not found")
	ErrInvalidConfig = errors.New("invalid cache configuration")
	ErrNotFound      = errors.New("not found in any layer")
)
