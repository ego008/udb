package udb

import (
	"context"
	"testing"
)

func TestV532AccessPathDiagnostics(t *testing.T) {
	point := planReadKeysFast([][]byte{[]byte("b"), []byte("a")}, defaultReadPlannerOptions())
	if point.Path != ReadPathPoint || point.AccessPath != ReadAccessPoint {
		t.Fatalf("unexpected point plan: %+v", point)
	}

	cursor := planReadKeysFast([][]byte{[]byte("k-0001"), []byte("k-0002"), []byte("k-0003"), []byte("k-0004"), []byte("k-0005"), []byte("k-0006"), []byte("k-0007"), []byte("k-0008"), []byte("k-0009"), []byte("k-0010"), []byte("k-0011"), []byte("k-0012"), []byte("k-0013"), []byte("k-0014"), []byte("k-0015"), []byte("k-0016")}, defaultReadPlannerOptions())
	if cursor.Path != ReadPathCursor || cursor.AccessPath != ReadAccessAdaptive || cursor.CursorStart != ReadCursorAdaptive {
		t.Fatalf("unexpected cursor plan: %+v", cursor)
	}
}

func TestV532PlannerCursorStartOverride(t *testing.T) {
	keys := [][]byte{[]byte("k-0001"), []byte("k-0002"), []byte("k-0003"), []byte("k-0004")}
	for _, tc := range []struct {
		name  string
		start ReadCursorStart
		want  ReadAccessPath
	}{
		{"first", ReadCursorFirst, ReadAccessFirst},
		{"seek", ReadCursorSeek, ReadAccessSeek},
		{"adaptive", ReadCursorAdaptive, ReadAccessAdaptive},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := defaultReadPlannerOptions()
			opts.MinCursorKeys = 2
			opts.CursorStart = tc.start
			p := planReadKeysFast(keys, opts)
			if p.Path != ReadPathCursor || p.CursorStart != tc.start || p.AccessPath != tc.want {
				t.Fatalf("plan=%+v", p)
			}
		})
	}
}

func TestV532ZeroCursorStartNormalizesToAdaptive(t *testing.T) {
	opts := defaultReadPlannerOptions()
	opts.CursorStart = 0
	opts.MinCursorKeys = 2
	p := planReadKeysFast([][]byte{[]byte("a"), []byte("b")}, opts)
	if p.CursorStart != ReadCursorAdaptive || p.AccessPath != ReadAccessAdaptive {
		t.Fatalf("plan=%+v", p)
	}
}

func TestV532ReadSemanticsUnchanged(t *testing.T) {
	db := openV57TestDB(t)
	defer db.Close()
	for i := 0; i < 64; i++ {
		key := []byte("k-" + pad8(i))
		if err := db.HSet("h", key, []byte("v-"+pad8(i))); err != nil {
			t.Fatal(err)
		}
		if err := db.ZSet("z", key, uint64(i)); err != nil {
			t.Fatal(err)
		}
	}

	keys := [][]byte{[]byte("k-00000020"), []byte("k-00000021"), []byte("k-00000021"), []byte("k-00000022"), []byte("k-99999999")}
	var gotH []string
	if err := db.HGetManyContext(context.Background(), "h", keys, func(i int, key, value []byte, found bool) error {
		gotH = append(gotH, string(key)+":"+string(value)+":"+boolString(found))
		if i != len(gotH)-1 {
			t.Fatalf("callback order: i=%d n=%d", i, len(gotH))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	wantH := []string{
		"k-00000020:v-00000020:true",
		"k-00000021:v-00000021:true",
		"k-00000021:v-00000021:true",
		"k-00000022:v-00000022:true",
		"k-99999999::false",
	}
	if !equalStrings(gotH, wantH) {
		t.Fatalf("hash result=%v want=%v", gotH, wantH)
	}

	var gotZ []uint64
	var found []bool
	if err := db.ZScoreManyContext(context.Background(), "z", keys, func(_ int, _ []byte, score uint64, ok bool) error {
		gotZ = append(gotZ, score)
		found = append(found, ok)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	wantZ := []uint64{20, 21, 21, 22, 0}
	for i := range wantZ {
		if gotZ[i] != wantZ[i] || found[i] != (i < 4) {
			t.Fatalf("z result=%v found=%v", gotZ, found)
		}
	}

}

func pad8(i int) string {
	s := "00000000" + itoa(i)
	return s[len(s)-8:]
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	buf := [20]byte{}
	p := len(buf)
	for i > 0 {
		p--
		buf[p] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[p:])
}

func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
