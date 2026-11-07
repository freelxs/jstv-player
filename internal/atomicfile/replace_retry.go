package atomicfile

import "time"

// retryReplace retries only errors that the operating system identifies as
// transient. The caller decides which platform errors are safe to retry.
func retryReplace(replace func() error, transient func(error) bool) error {
	delay := time.Millisecond
	for attempt := 0; ; attempt++ {
		err := replace()
		if err == nil || attempt == 9 || !transient(err) {
			return err
		}
		time.Sleep(delay)
		if delay < 64*time.Millisecond {
			delay *= 2
		}
	}
}
