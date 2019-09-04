package mnemosyne

import (
	"encoding/json"
	"time"
)

type cachable struct {
	Time         time.Time
	CachedObject interface{}
}

type cachableRet struct {
	Time         time.Time
	CachedObject *json.RawMessage
}
