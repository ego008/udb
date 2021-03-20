# udb
A Bolt wrapper, optimization for youdb https://github.com/ego008/youdb

## example

``` go
db.Update(func(tx *bolt.Tx) error {
    db.Tx = tx
    db.Hset("test", []byte("k"), []byte("v"))
    db.Hget("test", []byte("k"))
    return nil
})


db.View(func(tx *bolt.Tx) error {
    db.Tx = tx
    db.Hget("test", []byte("k"))
    db.Hget("test", []byte("k2"))
    return nil
})
```

or

``` go
db.Update2(func() error {
    db.Hset("test", []byte("k"), []byte("v"))
    db.Hget("test", []byte("k"))
    return nil
})

db.View2(func() error {
    db.Hget("test", []byte("k"))
    db.Hget("test", []byte("k2"))
    return nil
})
```

see more https://youbbs.org/t/3280
