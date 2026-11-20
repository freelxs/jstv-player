package atomicfile

import "time"

// retryReplace retries only errors that the operating system identifies as
// transient. The caller decides which platform errors are safe to retry.
func retryReplace(replace func() error, transient func(error) bool) error {
	return retryN(10, replace, transient)
}

func retryN(max int, replace func() error, transient func(error) bool) error {
	if max < 1 {
		max = 1
	}
	delay := time.Millisecond
	for attempt := 0; ; attempt++ {
		err := replace()
		if err == nil || attempt == max-1 || !transient(err) {
			return err
		}
		time.Sleep(delay)
		if delay < 64*time.Millisecond {
			delay *= 2
		}
	}
}
