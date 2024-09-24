package database

import (
	"fmt"

	"github.com/massmux/SatsMobiBot/internal/lnbits"
	"github.com/massmux/SatsMobiBot/internal/str"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

func MigrateAnonIdInt32Hash(db *gorm.DB) error {
	users := []lnbits.User{}
	_ = db.Find(&users)
	for _, u := range users {
		log.Infof("[MigrateAnonIdInt32Hash] %d -> %d", u.ID, str.Int32Hash(u.ID))
		u.AnonID = fmt.Sprint(str.Int32Hash(u.ID))
		tx := db.Save(u)
		if tx.Error != nil {
			errmsg := fmt.Sprintf("[MigrateAnonIdInt32Hash] Error: Couldn't migrate user %s (%d)", u.Telegram.Username, u.Telegram.ID)
			log.Errorln(errmsg)
			return tx.Error
		}
	}
	return nil
}

func MigrateAnonIdSha265Hash(db *gorm.DB) error {
	users := []lnbits.User{}
	_ = db.Find(&users)
	for _, u := range users {
		pw := u.Wallet.ID
		anon_id := str.AnonIdSha256(&u)
		log.Infof("[MigrateAnonIdSha265Hash] %s -> %s", pw, anon_id)
		u.AnonIDSha256 = anon_id
		tx := db.Save(u)
		if tx.Error != nil {
			errmsg := fmt.Sprintf("[MigrateAnonIdSha265Hash] Error: Couldn't migrate user %s (%s)", u.Telegram.Username, pw)
			log.Errorln(errmsg)
			return tx.Error
		}
	}
	return nil
}

func MigrateUUIDSha265Hash(db *gorm.DB) error {
	users := []lnbits.User{}
	_ = db.Find(&users)
	for _, u := range users {
		pw := u.Wallet.ID
		uuid := str.UUIDSha256(&u)
		log.Infof("[MigrateUUIDSha265Hash] %s -> %s", pw, uuid)
		u.UUID = uuid
		tx := db.Save(u)
		if tx.Error != nil {
			errmsg := fmt.Sprintf("[MigrateUUIDSha265Hash] Error: Couldn't migrate user %s (%s)", u.Telegram.Username, pw)
			log.Errorln(errmsg)
			return tx.Error
		}
	}
	return nil
}
