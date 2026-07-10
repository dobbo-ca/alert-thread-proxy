package slack

import "time"

type retryClient struct {
	c        Client
	attempts int
	base     time.Duration
	sleep    func(time.Duration)
}

// WithRetry wraps c with bounded exponential backoff. sleep is injectable so
// tests don't actually wait. On exhaustion the last error is returned (caller
// logs + drops — never blocks ingest).
func WithRetry(c Client, attempts int, base time.Duration, sleep func(time.Duration)) Client {
	return &retryClient{c: c, attempts: attempts, base: base, sleep: sleep}
}

func (r *retryClient) PostMessage(text, threadTS string) (string, error) {
	var ts string
	var err error
	d := r.base
	for i := 0; i < r.attempts; i++ {
		ts, err = r.c.PostMessage(text, threadTS)
		if err == nil {
			return ts, nil
		}
		if i < r.attempts-1 {
			r.sleep(d)
			d *= 2
		}
	}
	return "", err
}

func (r *retryClient) UpdateMessage(ts, text string) error {
	var err error
	d := r.base
	for i := 0; i < r.attempts; i++ {
		err = r.c.UpdateMessage(ts, text)
		if err == nil {
			return nil
		}
		if i < r.attempts-1 {
			r.sleep(d)
			d *= 2
		}
	}
	return err
}
