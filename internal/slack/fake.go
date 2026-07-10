package slack

import "sync"

type Call struct {
	Method   string // "post" or "update"
	Text     string
	ThreadTS string // for post
	TS       string // for update
}

type Fake struct {
	mu       sync.Mutex
	Calls    []Call
	nextTS   int
	FailNext bool
}

func (f *Fake) PostMessage(text, threadTS string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FailNext {
		f.FailNext = false
		return "", errFake
	}
	f.nextTS++
	ts := tsString(f.nextTS)
	f.Calls = append(f.Calls, Call{Method: "post", Text: text, ThreadTS: threadTS})
	return ts, nil
}

func (f *Fake) UpdateMessage(ts, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, Call{Method: "update", TS: ts, Text: text})
	return nil
}
