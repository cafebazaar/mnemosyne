package mnemosyne

import "time"

type ICounter interface {
	Inc(...string)
}

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
func (*DummyTimer) Done(a time.Time, b ...string) {

}
func (*DummyCounter) Inc(...string) {
}
