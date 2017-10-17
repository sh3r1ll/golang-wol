package storage

import (
	"bytes"
	"encoding/gob"
	"errors"

	"golang.org/x/crypto/bcrypt"

	"bitbucket.org/cmaiorano/golang-wol/types"
	storage "github.com/coreos/bbolt"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

const (
	devicesBucket  = "DevBucket"
	passwordBucket = "PassBucket"
	passworkdKey   = "AdminPassword"
	dbName         = "rwol.db"
	defaultDbLoc   = "storage"
)

var db *storage.DB

func init() {
	log.SetLevel(log.DebugLevel)
}

//StartHandling start an infinite loop in order to handle properly the bbolt database used for alias and password storage
func StartHandling(deviceChan chan *types.Alias, getChan chan *types.GetDev, passHandlingChan chan *types.PasswordHandling, updatePassChan chan *types.PasswordUpdate, getAliases chan chan string) {
	if db == nil {
		db = getDB()
	}

	for {
		select {
		case newDev := <-deviceChan:
			log.Debugf("%v", newDev)
			err := addDevice(newDev.Device, newDev.Name)
			if err != nil {
				close(newDev.Response)
			} else {
				newDev.Response <- struct{}{}
				close(newDev.Response)
			}
		case getDev := <-getChan:
			log.Debug("%v", getDev)
			device, err := getDevice(getDev.Alias)
			if err != nil {
				close(getDev.Response)
			} else {
				getDev.Response <- device
				close(getDev.Response)
			}
		case passHandling := <-passHandlingChan:
			log.Debugf("%v", passHandling)
			err := checkPassword(passHandling.Password)
			passHandling.Response <- err
			close(passHandling.Response)

		case updatePass := <-updatePassChan:
			log.Debug("%v", updatePass)
			err := updatePassword(updatePass.OldPassword, updatePass.NewPassword)
			updatePass.Response <- err
			close(updatePass.Response)

		case aliasChan := <-getAliases:
			log.Debug("Got all alias request")
			getAliasesFromStorage(aliasChan)
			close(aliasChan)
		}
	}
}

//InitLocal initialize db in case is first start of web application
func InitLocal(initialPassword string) {

	db = getDB()
	log.Debugf("Openend database %v, starting bucket definition", db)

	err := db.Update(func(transaction *storage.Tx) error {
		if _, createErr := transaction.CreateBucketIfNotExists([]byte(devicesBucket)); createErr != nil {
			log.Errorf("Error creating devicesBucket: %v", createErr)
			return createErr
		}
		if _, createErr := transaction.CreateBucketIfNotExists([]byte(passwordBucket)); createErr != nil {
			log.Errorf("Error creating passwordBucket: %v", createErr)
			return createErr
		}
		return nil
	})

	if err != nil {
		log.Errorf("Got err %v, panic!!!", err)
		panic(err)
	}

	err = insertPassword(initialPassword, false)

	if err != nil {
		panic(err)
	}
}

func getDB() *storage.DB {
	dbLoc := defaultDbLoc
	if viper.IsSet("storage.path") {
		dbLoc = viper.GetString("storage.path")
	}

	localDB, err := storage.Open(dbLoc+"/"+dbName, 0600, nil)
	if err != nil {
		panic(err)
	}
	return localDB
}

func addDevice(device *types.Device, name string) error {
	log.Debugf("Adding device %v with name %s", device, name)
	buf, err := encodeFromMacIfaceIP(device.Mac, device.Iface, device.IP)

	if err != nil {
		log.Errorf("Got error encoding: %v", err)
		return err
	}

	err = db.Update(func(transaction *storage.Tx) error {
		bucket := transaction.Bucket([]byte(devicesBucket))
		err := bucket.Put([]byte(name), buf.Bytes())
		log.Debugf("Error? %v", err)
		return err
	})
	return err
}

func getAliasesFromStorage(aliasChan chan string) {
	log.Debugf("Got channel %v for alias retrieving", aliasChan)

	db.View(func(transaction *storage.Tx) error {
		cursor := transaction.Bucket([]byte(devicesBucket)).Cursor()
		for k, _ := cursor.First(); k != nil; k, _ = cursor.Next() {
			log.Debugf("Device %s", string(k))
			aliasChan <- string(k)
		}
		return nil
	})
}

func getDevice(name string) (*types.Device, error) {
	device := &types.Device{}
	log.Debugf("Getting data for device with alias %s", name)

	err := db.View(func(transaction *storage.Tx) error {
		bucket := transaction.Bucket([]byte(devicesBucket))
		dev := bucket.Get([]byte(name))
		reader := bytes.NewReader(dev)
		err := gob.NewDecoder(reader).Decode(&device)

		if err != nil {
			log.Errorf("Got error decoding: %v", err)
			return err
		}
		return nil
	})

	if err != nil {
		return nil, err
	}
	log.Debugf("Device is: %v", device)
	return device, nil
}

func checkPassword(pass string) error {

	err := db.View(func(transaction *storage.Tx) error {
		bucket := transaction.Bucket([]byte(passwordBucket))
		savedPass := bucket.Get([]byte(passworkdKey))
		log.Debugf("Got %s for password from bucket", string(savedPass))
		return bcrypt.CompareHashAndPassword(savedPass, []byte(pass))
	})
	return err
}

func insertPassword(pass string, update bool) error {
	log.Debugf("Password is %v", pass)
	passHash := []byte(pass)
	effectivePasswd, err := bcrypt.GenerateFromPassword(passHash, bcrypt.DefaultCost)

	if err != nil {
		panic(err)
	}

	err = db.Update(func(transaction *storage.Tx) error {
		bucket := transaction.Bucket([]byte(passwordBucket))
		if bucket.Get([]byte(passworkdKey)) != nil && !update {
			return errors.New("Password already defined")
		}

		err := bucket.Put([]byte(passworkdKey), effectivePasswd)

		return err
	})

	return err
}

func updatePassword(oldPassword, newPassword string) error {

	err := db.Update(func(transaction *storage.Tx) error {
		bucket := transaction.Bucket([]byte(passwordBucket))
		effectiveOldPassHash := bucket.Get([]byte(passworkdKey))
		err := bcrypt.CompareHashAndPassword(effectiveOldPassHash, []byte(effectiveOldPassHash))
		if err != nil {
			log.Errorf("Got error %v", err)
			return err
		}
		err = insertPassword(newPassword, true)
		return err
	})
	return err
}

func encodeFromMacIfaceIP(mac, iface, IPAddr string) (*bytes.Buffer, error) {
	buf := bytes.NewBuffer(nil)
	entry := types.Device{Mac: mac, Iface: iface, IP: IPAddr}
	err := gob.NewEncoder(buf).Encode(entry)
	return buf, err
}
