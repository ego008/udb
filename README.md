# udb
A Bolt wrapper, optimization for youdb https://github.com/ego008/youdb

## example

``` go
db.Update(func(tx *bolt.Tx) error {
    db.Hset(tx, "test", []byte("k"), []byte("v"))
    db.Hget(tx, "test", []byte("k"))
    return nil
})


db.View(func(tx *bolt.Tx) error {
    db.Hget(tx, "test", []byte("k"))
    db.Hget(tx, "test", []byte("k2"))
    return nil
})
```

see more https://youbbs.org/t/3280
