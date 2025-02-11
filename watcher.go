package mnemosyne

import "time"

// ICounter defines the interface for counting operations
type ICounter interface {
	Inc(...string)
}

// ITimer defines the interface for timing operations
type ITimer interface {
	Start() time.Time
	Done(time.Time, ...string)
}

type DummyTimer struct {
}

type DummyCounter struct {
}

func NewDummyCounter() *DummyCounter {
	return &DummyCounter{}
}

func NewDummyTimer() *DummyTimer {
	return &DummyTimer{}
}
func (*DummyTimer) Start() time.Time {
	return time.Now()
}
func (*DummyTimer) Done(time.Time, ...string) {

}
func (*DummyCounter) Inc(...string) {
}
