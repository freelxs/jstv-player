package atomicfile

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestReadersSeeOnlyCompleteVersions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	a, b := bytes.Repeat([]byte("a"), 32000), bytes.Repeat([]byte("b"), 64000)
	if err := Write(path, a, 0o600); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	done := make(chan struct{})
	fail := make(chan string, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			data, err := os.ReadFile(path)
			if err != nil || (!bytes.Equal(data, a) && !bytes.Equal(data, b)) {
				select {
				case fail <- "reader saw missing or partial data":
				default:
				}
				return
			}
		}
	}()
	for i := 0; i < 30; i++ {
		data := a
		if i%2 == 0 {
			data = b
		}
		if err := Write(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	close(done)
	wg.Wait()
	select {
	case message := <-fail:
		t.Fatal(message)
	default:
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatal("temporary files leaked")
	}
}
