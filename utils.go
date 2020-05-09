package mnemosyne

import (
	"fmt"
	"strings"
)

type AmnesiaError struct {
	Chance int
}

func (e *AmnesiaError) Error() string {
	return fmt.Sprintf("Had Amnesia (Chance:%d)", e.Chance)
}
func NewAmnesiaError(c int) *AmnesiaError {
	return &AmnesiaError{Chance: c}
}

func MakeKey(keys ...string) string {
	return strings.Join(keys, ";")
}
