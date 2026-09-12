package main

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"  // 注册 "sqlite" 驱动，只副作用导入
)

// StoredMessage 是落库后的一条消息。payload 存原始 JSON 文本，
// 补投时原样回放——store 不理解协议内容，只管存取。
type StoredMessage struct {
	Mid string
	QID string
	Payload string// SEND 的原始 payload JSON
	TS int64
}

// Store 是 M2 的真相源。DB 里的数据比内存活得久，
// hub 的 map 从此降级为"谁在线"的缓存。
type Store struct {
	db *sql.DB
}

// Open 打开（或创建）数据库并建表。
// 幂等：CREATE TABLE IF NOT EXISTS，进程重启随便调。
func Open(path string)(*Store,error){
	db,err := sql.Open("sqlite",path)
	if err != nil {
		return nil,fmt.Errorf("open db: %w",err)
	}

	// 三个 pragma 各有用途：
	// WAL：写不阻塞读，掉电只丢最后一个事务，比默认 journal 健壮
	// busy_timeout：并发写时的等待窗口，报 SQLITE_BUSY 前先等 5s
	// foreign_keys：sqlite 默认关闭外键约束，必须显式开
	for _,p := range []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA busy_timeout=5000`,
		`PRAGMA foreign_keys=ON`,
	}{
		if _,err := db.Exec(p);err != nil {
			db.Close()
			return nil,fmt.Errorf("pragma %q: %w",p,err)
		}
	}

	s := &Store{db:db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil,err
	}
	return s,nil
}


// migrate 建表。schema 变更永远走这里追加新迁移，不改历史语句
func (s *Store) migrate() error {
		const schema = `
CREATE TABLE IF NOT EXISTS queues (
    qid        TEXT PRIMARY KEY,
    created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS messages (
    mid          TEXT PRIMARY KEY,
    qid          TEXT NOT NULL REFERENCES queues(qid),
    payload      TEXT NOT NULL,
    ts           INTEGER NOT NULL,
    delivered    INTEGER NOT NULL DEFAULT 0,
    delivered_at INTEGER
);

CREATE INDEX IF NOT EXISTS idx_messages_qid_undelivered
    ON messages(qid, delivered, ts);
`
	if _,err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("migrate: %w",err)
	}
	return nil
}

func (s *Store) Close() error {return s.db.Close()}

// CreateQueue 建队列记录。INSERT OR IGNORE：重复创建不报错
// （NEW 重试、重连后重复 NEW 都安全）。
func (s *Store) CreateQueue(qid string,now int64) error {
	_,err := s.db.Exec(
		`INSERT OR IGNORE INTO queues(qid,created_at) VALUES(?,?)`,
		qid,now,
	)
	if err != nil {	
		return fmt.Errorf("create queue: %w",err)
	}
	return nil 
} 
