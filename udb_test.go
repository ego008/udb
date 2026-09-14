package udb

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestHashAndZSet(t *testing.T) {
	o := DefaultOptions()
	o.Maintenance.Enabled = false
	db, err := OpenWithOptions(t.TempDir()+"/test.db", &o)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.Update(func(tx *Tx) error {
		if err := db.Hset(tx, "h", []byte("a"), []byte("hello")); err != nil {
			return err
		}
		if _, err := db.Hincr(tx, "counter", []byte("n"), 3); err != nil {
			return err
		}
		if err := db.Zset(tx, "rank", []byte("a"), 10); err != nil {
			return err
		}
		if err := db.Zset(tx, "rank", []byte("b"), 10); err != nil {
			return err
		}
		return db.Zset(tx, "rank", []byte("c"), 20)
	}); err != nil {
		t.Fatal(err)
	}

	if err := db.View(func(tx *Tx) error {
		if got := db.Hget(tx, "h", []byte("a")).String(); got != "hello" {
			t.Fatalf("Hget=%q", got)
		}
		v, err := db.HgetInt(tx, "counter", []byte("n"))
		if err != nil || v != 3 {
			t.Fatalf("counter=%d err=%v", v, err)
		}
		r := db.Zscan(tx, "rank", []byte("a"), I2b(10), 10)
		want := []Entry{{Key: BS("b"), Value: BS(I2b(10))}, {Key: BS("c"), Value: BS(I2b(20))}}
		if !reflect.DeepEqual(r.List(), want) {
			t.Fatalf("Zscan=%v want=%v", r.List(), want)
		}
		r = db.Zrscan(tx, "rank", []byte("c"), I2b(20), 10)
		want = []Entry{{Key: BS("b"), Value: BS(I2b(10))}, {Key: BS("a"), Value: BS(I2b(10))}}
		if !reflect.DeepEqual(r.List(), want) {
			t.Fatalf("Zrscan=%v want=%v", r.List(), want)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestHighLevelAPI(t *testing.T) {
	o := DefaultOptions()
	o.Maintenance.Enabled = false
	db, err := OpenWithOptions(t.TempDir()+"/api.db", &o)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.HSet("h", []byte("k"), []byte("v")); err != nil {
		t.Fatal(err)
	}
	if got := db.HGet("h", []byte("k")).String(); got != "v" {
		t.Fatalf("got %q", got)
	}
	if err := db.ZSet("z", []byte("m"), 123); err != nil {
		t.Fatal(err)
	}
	if got := db.ZGet("z", []byte("m")).Uint64(); got != 123 {
		t.Fatalf("got %d", got)
	}
}

func TestMaintenanceGate(t *testing.T) {
	o := DefaultOptions()
	o.Maintenance.Enabled = false
	db, err := OpenWithOptions(t.TempDir()+"/gate.db", &o)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- db.View(func(tx *Tx) error { close(started); <-release; return nil }) }()
	<-started
	go func() { time.Sleep(20 * time.Millisecond); close(release) }()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestManagerContext(t *testing.T) {
	o := DefaultOptions()
	o.Maintenance.Interval = time.Hour
	db, err := OpenWithOptions(t.TempDir()+"/ctx.db", &o)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := db.StartMaintenance(ctx); err == nil {
		t.Fatal("expected busy because Open starts manager")
	}
	cancel()
	db.Close()
}

func TestConcurrentViewsAreNotSerializedByUDBMutex(t *testing.T) {
	o := DefaultOptions()
	o.Maintenance.Enabled = false
	db, err := OpenWithOptions(t.TempDir()+"/concurrent-view.db", &o)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	done := make(chan error, 2)

	for i := 0; i < 2; i++ {
		go func() {
			done <- db.View(func(tx *Tx) error {
				entered <- struct{}{}
				<-release
				return nil
			})
		}()
	}

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first View did not start")
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("second View was serialized; bbolt read transactions should run concurrently")
	}
	close(release)
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func TestCloseWaitsForManagedOperation(t *testing.T) {
	o := DefaultOptions()
	o.Maintenance.Enabled = false
	db, err := OpenWithOptions(t.TempDir()+"/close-wait.db", &o)
	if err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	viewDone := make(chan error, 1)
	closeDone := make(chan error, 1)

	go func() {
		viewDone <- db.View(func(tx *Tx) error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started

	go func() { closeDone <- db.Close() }()

	select {
	case err := <-closeDone:
		t.Fatalf("Close returned before active operation finished: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	if err := <-viewDone; err != nil {
		t.Fatal(err)
	}
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
	if err := db.View(func(tx *Tx) error { return nil }); err != ErrDatabaseClosed {
		t.Fatalf("View after Close = %v, want %v", err, ErrDatabaseClosed)
	}
}
