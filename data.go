package mnemosyne

import (
	"encoding/json"
	"time"
)

type cachable struct {
	Time         time.Time
	CahcedObject interface{}
}

type cachableRet struct {
	Time         time.Time
	CahcedObject *json.RawMessage
}
