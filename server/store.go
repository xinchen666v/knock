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

// QueueExists 队列是否存在——404 判定的唯一依据（Step 2 起替代内存 map）
func (s *Store) QueueExists(qid string) (bool,error) {
	var one int 
	err := s.db.QueryRow(`SELECT 1 FROM queues WHERE qid = ?`,qid).Scan(&one)
	if err == sql.ErrNoRows {
		return false,nil
	}
	if err != nil {
		return false,fmt.Errorf("queue exists: %w",err)
	}
	return true,nil
}


// SaveMessage 落一条消息。mid 是主键，重复 mid 会报错——
// 这是数据库级的去重保险，比应用层去重更硬。
func (s *Store) SaveMessage(m StoredMessage) error {
	_, err := s.db.Exec(
		`INSERT INTO messages (mid, qid, payload, ts) VALUES (?, ?, ?, ?)`,
		m.Mid, m.QID, m.Payload, m.TS)
	if err != nil {
		return fmt.Errorf("save message: %w", err)
	}
	return nil
}


// Undelivered 某队列未投递的消息，按时间升序——补投顺序的保证。
// ORDER BY ts 相同则按 rowid（插入序）稳定排序。
func (s *Store) Undelivered(qid string) ([]StoredMessage,error) {
	rows,err := s.db.Query(`
		SELECT mid, qid, payload, ts FROM messages
		WHERE qid = ? AND delivered = 0
		ORDER BY ts, rowid`,qid)
	if err != nil {
		return nil,fmt.Errorf("undelivered: %w",err)
	}
	defer rows.Close()

	var out []StoredMessage
	for rows.Next() {
		var m StoredMessage
		if err := rows.Scan(&m.Mid,&m.QID,&m.Payload,&m.TS); err != nil {
			return nil,fmt.Errorf("scan message: %w",err)
		}
		out = append(out,m)
	}
	return out,rows.Err()
}


// MarkDelivered 投递完成打标。不删行——ACK 丢失时重投靠它兜底，
// 清理交给后面的 cleanupLoop（M2 收尾的甜点）。
func (s *Store) MarkDelivered(mid string) error{
	_,err := s.db.Exec(
		`UPDATE messages SET delivered = 1, delivered_at = strftime('%s','now')
		 WHERE mid = ?`, mid)
	if err != nil {
		return fmt.Errorf("mark delivered: %w",err)
	}
	return nil
}


