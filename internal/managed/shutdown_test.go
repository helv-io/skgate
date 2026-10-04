package managed

import (
	"context"
	"testing"
	"time"
)

// A background job (an update) that starts its process again after Shutdown has stopped it must not leave that
// process running: Shutdown waits for the job and then stops everything once more.
func TestShutdownStopsWhatABackgroundJobStartedAfterTheStop(t *testing.T) {
	m, _ := testMgr(t, nil)
	p, _ := m.Proc(fakeSpec("a"))
	initSession(t, serve(t, p).URL, nil)
	first := p.Status().PID
	if first == 0 {
		t.Fatal("not running")
	}
	if !m.bg(func(ctx context.Context) {
		<-ctx.Done() // Shutdown has begun
		for p.Status().State != StateStopped {
			time.Sleep(5 * time.Millisecond) // its first stop is done
		}
		_ = p.Start()
	}) {
		t.Fatal("manager closed")
	}
	m.Shutdown()
	if st := p.Status(); st.State != StateStopped || st.PID != 0 {
		t.Fatalf("a process was left behind: %+v", st)
	}
	waitFor2(t, "group gone", 4*time.Second, func() bool { return !groupAlive(first) })
}
