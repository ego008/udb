package udb

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestMaintenanceManagerLifecycle(t *testing.T) {
	o := DefaultOptions()
	o.Maintenance.Enabled = true
	o.Maintenance.Interval = time.Hour
	db, err := OpenWithOptions(t.TempDir()+"/m.db", &o)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.StartMaintenance(context.Background()); err == nil {
		t.Fatal("second start should fail")
	}
	db.StopMaintenance()
	db.StopMaintenance()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestHealth(t *testing.T) {
	o := DefaultOptions()
	o.Maintenance.Enabled = false
	db, err := OpenWithOptions(t.TempDir()+"/h.db", &o)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := db.Health()
	if h.Closed || !h.OK {
		t.Fatalf("bad health: %+v", h)
	}
}

func TestLifecycleAllowsConcurrentManagedViews(t *testing.T) {
	o := DefaultOptions()
	o.Maintenance.Enabled = false
	db, err := OpenWithOptions(t.TempDir()+"/concurrent.db", &o)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			err := db.View(func(tx *Tx) error {
				entered <- struct{}{}
				<-release
				return nil
			})
			if err != nil {
				t.Errorf("View failed: %v", err)
			}
		}()
	}

	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first View did not enter")
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("second View did not enter concurrently; lifecycle lock is serializing transactions")
	}
	close(release)
	wg.Wait()
}

func TestLifecycleCloseWaitsForManagedOperation(t *testing.T) {
	o := DefaultOptions()
	o.Maintenance.Enabled = false
	db, err := OpenWithOptions(t.TempDir()+"/close.db", &o)
	if err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	viewDone := make(chan error, 1)
	go func() {
		viewDone <- db.View(func(tx *Tx) error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started

	closeDone := make(chan error, 1)
	go func() { closeDone <- db.Close() }()

	select {
	case err := <-closeDone:
		t.Fatalf("Close returned before active managed operation finished: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	if err := <-viewDone; err != nil {
		t.Fatal(err)
	}
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
}
