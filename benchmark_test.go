package udb

import (
	"fmt"
	"testing"
)

func BenchmarkHashUpdate(b *testing.B) {
	o := DefaultOptions()
	o.Maintenance.Enabled = false
	db, err := OpenWithOptions(b.TempDir()+"/bench.db", &o)
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := db.Update(func(tx *Tx) error {
			return db.Hset(tx, "bench", []byte(fmt.Sprintf("k-%d", i%1000)), []byte("value"))
		}); err != nil {
			b.Fatal(err)
		}
	}
}
