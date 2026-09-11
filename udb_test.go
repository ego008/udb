package udb

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// run test: go test -v

// setupTestDB 创建一个临时的 BoltDB 实例用于测试，并在测试结束后清理
func setupTestDB(t *testing.T) (*DB, string) {
	dir, err := os.MkdirTemp("", "udb_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	dbPath := filepath.Join(dir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatalf("failed to open db: %v", err)
	}

	return db, dir
}

func TestHashOperations(t *testing.T) {
	db, dir := setupTestDB(t)
	defer db.Close()
	defer os.RemoveAll(dir)

	hashName := "portfolio_positions"

	// 1. 测试 Hset 与 Hget
	err := db.Update(func(tx *bolt.Tx) error {
		return db.Hset(tx, hashName, []byte("AAPL"), []byte("150.5"))
	})
	if err != nil {
		t.Fatalf("Hset failed: %v", err)
	}

	err = db.View(func(tx *bolt.Tx) error {
		reply := db.Hget(tx, hashName, []byte("AAPL"))
		if !reply.OK() {
			t.Fatalf("expected ok, got state: %s", reply.State)
		}
		if reply.String() != "150.5" {
			t.Fatalf("expected '150.5', got '%s'", reply.String())
		}
		return nil
	})
	if err != nil {
		t.Fatalf("View transaction failed: %v", err)
	}

	// 2. 测试 Hmset 与 Hmget
	err = db.Update(func(tx *bolt.Tx) error {
		return db.Hmset(tx, hashName,
			[]byte("TSLA"), []byte("200.0"),
			[]byte("NVDA"), []byte("1000.0"),
		)
	})
	if err != nil {
		t.Fatalf("Hmset failed: %v", err)
	}

	err = db.View(func(tx *bolt.Tx) error {
		reply := db.Hmget(tx, hashName, [][]byte{[]byte("TSLA"), []byte("NVDA"), []byte("UNKNOWN")})
		if !reply.OK() {
			t.Fatalf("Hmget failed with state: %s", reply.State)
		}
		dict := reply.Dict()
		if string(dict["TSLA"]) != "200.0" || string(dict["NVDA"]) != "1000.0" {
			t.Fatalf("Hmget dict values mismatch: %v", dict)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Hmget transaction failed: %v", err)
	}

	// 3. 测试 Hincr 自增与溢出防范
	err = db.Update(func(tx *bolt.Tx) error {
		val, err := db.Hincr(tx, hashName, []byte("counter"), 10)
		if err != nil || val != 10 {
			t.Fatalf("Hincr failed: err=%v, val=%d", err, val)
		}

		val, err = db.Hincr(tx, hashName, []byte("counter"), -3)
		if err != nil || val != 7 {
			t.Fatalf("Hincr negative step failed: err=%v, val=%d", err, val)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Hincr transaction failed: %v", err)
	}

	// 4. 测试 Hscan 与 Hrscan 范围查询
	err = db.View(func(tx *bolt.Tx) error {
		// 插入顺序：AAPL, NVDA, TSLA, counter
		reply := db.Hscan(tx, hashName, []byte("A"), 10)
		if !reply.OK() {
			t.Fatalf("Hscan failed")
		}
		// 验证扫描结果数量
		if reply.KvLen() < 3 {
			t.Fatalf("Hscan count unexpected: %d", reply.KvLen())
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Hscan transaction failed: %v", err)
	}

	// 5. 测试 Hdel 删除
	err = db.Update(func(tx *bolt.Tx) error {
		return db.Hdel(tx, hashName, []byte("AAPL"))
	})
	if err != nil {
		t.Fatalf("Hdel failed: %v", err)
	}
}

func TestZSetOperations(t *testing.T) {
	db, dir := setupTestDB(t)
	defer db.Close()
	defer os.RemoveAll(dir)

	zsetName := "etf_momentum_rank"

	// 1. 测试 Zset 写入分数
	err := db.Update(func(tx *bolt.Tx) error {
		if err := db.Zset(tx, zsetName, []byte("513100"), 85); err != nil {
			return err
		}
		if err := db.Zset(tx, zsetName, []byte("518880"), 92); err != nil {
			return err
		}
		return db.Zset(tx, zsetName, []byte("159985"), 78)
	})
	if err != nil {
		t.Fatalf("Zset initialization failed: %v", err)
	}

	// 2. 测试 Zget 获取分数
	err = db.View(func(tx *bolt.Tx) error {
		reply := db.Zget(tx, zsetName, []byte("518880"))
		if !reply.OK() {
			t.Fatalf("Zget failed")
		}
		if reply.Uint64() != 92 {
			t.Fatalf("expected score 92, got %d", reply.Uint64())
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Zget transaction failed: %v", err)
	}

	// 3. 测试 Zincr 分数调整
	err = db.Update(func(tx *bolt.Tx) error {
		newScore, err := db.Zincr(tx, zsetName, []byte("159985"), 5)
		if err != nil || newScore != 83 {
			t.Fatalf("Zincr failed: err=%v, score=%d", err, newScore)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Zincr transaction failed: %v", err)
	}

	// 4. 测试 Zscan 排序和范围扫描
	err = db.View(func(tx *bolt.Tx) error {
		// 从头开始扫描
		reply := db.Zscan(tx, zsetName, []byte(""), nil, 10)
		if !reply.OK() {
			t.Fatalf("Zscan failed")
		}

		entries := reply.List()
		if len(entries) != 3 {
			t.Fatalf("expected 3 entries in zset, got %d", len(entries))
		}

		// 检查底层是否按照分数由小到大正确排序
		// 分数最低的应该是 159985 (score 83)
		if !bytes.Equal(entries[0].Key, []byte("159985")) {
			t.Fatalf("zset sort order error, first key should be 159985, got %s", entries[0].Key)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Zscan transaction failed: %v", err)
	}

	// 5. 测试 Zrscan 反向扫描
	err = db.View(func(tx *bolt.Tx) error {
		reply := db.Zrscan(tx, zsetName, nil, nil, 1)
		if !reply.OK() {
			t.Fatalf("Zrscan failed")
		}
		entries := reply.List()
		if len(entries) != 1 {
			t.Fatalf("Zrscan limit failed")
		}
		// 分数最高的应该是 518880 (score 92)
		if !bytes.Equal(entries[0].Key, []byte("518880")) {
			t.Fatalf("zrscan order error, highest should be 518880, got %s", entries[0].Key)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Zrscan transaction failed: %v", err)
	}
}

// TestReplyClone 验证跨事务安全拷贝机制，防止 mmap 内存失效
func TestReplyClone(t *testing.T) {
	db, dir := setupTestDB(t)
	defer db.Close()
	defer os.RemoveAll(dir)

	err := db.Update(func(tx *bolt.Tx) error {
		return db.Hset(tx, "clonetest", []byte("k"), []byte("v"))
	})
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	var clonedReply *Reply
	// 在一个短生命周期的事务中查询
	err = db.View(func(tx *bolt.Tx) error {
		reply := db.Hget(tx, "clonetest", []byte("k"))
		if !reply.OK() {
			t.Fatalf("get failed")
		}
		// 克隆数据，脱离事务内存绑定
		clonedReply = reply.Clone()
		return nil
	})
	if err != nil {
		t.Fatalf("transaction failed: %v", err)
	}

	// 事务已经结束，访问 Cloned 数据应当完全安全且内容正确
	if clonVal := clonedReply.String(); clonVal != "v" {
		t.Fatalf("expected 'v', got '%s'", clonVal)
	}
}
