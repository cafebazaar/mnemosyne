package mnemosyne

import (
	"fmt"
	"strings"
)

type amnesiaError struct {
	Chance int
}

func (e *amnesiaError) Error() string {
	return fmt.Sprintf("Had Amnesia (Chance:%d)", e.Chance)
}
func newAmnesiaError(c int) *amnesiaError {
	return &amnesiaError{Chance: c}
}

func MakeKey(keys ...string) string {
	return strings.Join(keys, ";")
}
