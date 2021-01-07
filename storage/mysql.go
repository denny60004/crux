package storage

import (
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

const (
	// TableCruxData table name of crux data
	TableCruxData = "crux_data"
)

type sqlDB struct {
	dbPath string
	conn   *gorm.DB
}

// CruxData .
type CruxData struct {
	Key   []byte `gorm:"type:blob NOT NULL;" json:"key"`
	Value []byte `gorm:"type:blob NOT NULL;" json:"value"`
}

// InitMysql .
func InitMysql(dbPath string) (*sqlDB, error) {
	conn, err := gorm.Open(mysql.Open(dbPath), &gorm.Config{})
	db := &sqlDB{
		dbPath: dbPath,
		conn:   conn,
	}
	return db, err
}

func (db *sqlDB) Write(key *[]byte, value *[]byte) error {
	data := &CruxData{
		Key:   *key,
		Value: *value,
	}
	res := db.conn.Debug().Create(data)
	return res.Error
}

func (db *sqlDB) Read(key *[]byte) (*[]byte, error) {
	var value CruxData
	res := db.conn.Debug().Where("`key` = ?", *key).First(&value)
	if res.Error != nil {
		return nil, res.Error
	}
	return &(value.Value), nil
}

func (db *sqlDB) ReadAll(f func(key, value *[]byte)) error {
	var data []CruxData
	res := db.conn.Debug().Find(&data)
	if res.Error != nil {
		return res.Error
	}
	var i int64
	for i = 0; i < res.RowsAffected; i++ {
		key, value := data[i].Key, data[i].Value
		f(&key, &value)
	}
	return nil
}

func (db *sqlDB) Delete(key *[]byte) error {
	res := db.conn.Debug().Where("`key` = ?", *key).Delete(&CruxData{})
	return res.Error
}

func (db *sqlDB) Close() error {
	return nil
}
