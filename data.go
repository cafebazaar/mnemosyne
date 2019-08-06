package mnemosyne

import "time"

type cachable struct {
	Time         time.Time
	CahcedObject interface{}
}
