package main

import (
	"fmt"

	"github.com/gorilla/websocket"

	"github.com/lxc/lxd/lxd/migration"
	"github.com/lxc/lxd/shared"
	"github.com/lxc/lxd/shared/api"
	"github.com/lxc/lxd/shared/logger"
)

func NewStorageMigrationSource(storage storage) (*migrationSourceWs, error) {
	ret := migrationSourceWs{migrationFields{storage: storage}, make(chan bool, 1)}

	var err error
	ret.controlSecret, err = shared.RandomCryptoString()
	if err != nil {
		logger.Errorf("Failed to create migration source secrect for control websocket")
		return nil, err
	}

	ret.fsSecret, err = shared.RandomCryptoString()
	if err != nil {
		logger.Errorf("Failed to create migration source secrect for filesystem websocket")
		return nil, err
	}

	return &ret, nil
}

func (s *migrationSourceWs) DoStorage(migrateOp *operation) error {
	<-s.allConnected

	// Storage needs to start unconditionally now, since we need to
	// initialize a new storage interface.
	ourMount, err := s.storage.StoragePoolVolumeMount()
	if err != nil {
		logger.Errorf("Failed to mount storage volume")
		return err
	}
	if ourMount {
		defer s.storage.StoragePoolVolumeUmount()
	}

	// The protocol says we have to send a header no matter what, so let's
	// do that, but then immediately send an error.
	myType := s.storage.MigrationType()
	header := migration.MigrationHeader{
		Fs: &myType,
	}

	err = s.send(&header)
	if err != nil {
		logger.Errorf("Failed to send storage volume migration header")
		s.sendControl(err)
		return err
	}

	driver, fsErr := s.storage.StorageMigrationSource()
	if fsErr != nil {
		logger.Errorf("Failed to initialize new storage volume migration driver")
		s.sendControl(fsErr)
		return fsErr
	}

	err = s.recv(&header)
	if err != nil {
		logger.Errorf("Failed to receive storage volume migration header")
		s.sendControl(err)
		return err
	}

	bwlimit := ""
	if *header.Fs != myType {
		myType = migration.MigrationFSType_RSYNC
		header.Fs = &myType

		driver, _ = rsyncStorageMigrationSource()

		// Check if this storage pool has a rate limit set for rsync.
		poolwritable := s.storage.GetStoragePoolWritable()
		if poolwritable.Config != nil {
			bwlimit = poolwritable.Config["rsync.bwlimit"]
		}
	}

	abort := func(err error) error {
		driver.Cleanup()
		s.sendControl(err)
		return err
	}

	err = driver.SendStorageVolume(s.fsConn, migrateOp, bwlimit, s.storage)
	if err != nil {
		logger.Errorf("Failed to send storage volume")
		return abort(err)
	}

	msg := migration.MigrationControl{}
	err = s.recv(&msg)
	if err != nil {
		logger.Errorf("Failed to receive storage volume migration control message")
		s.disconnect()
		return err
	}

	if !*msg.Success {
		logger.Errorf("Failed to send storage volume")
		return fmt.Errorf(*msg.Message)
	}

	logger.Debugf("Migration source finished transferring storage volume")
	return nil
}

func NewStorageMigrationSink(args *MigrationSinkArgs) (*migrationSink, error) {
	sink := migrationSink{
		src:    migrationFields{storage: args.Storage},
		dest:   migrationFields{storage: args.Storage},
		url:    args.Url,
		dialer: args.Dialer,
		push:   args.Push,
	}

	if sink.push {
		sink.allConnected = make(chan bool, 1)
	}

	var ok bool
	var err error
	if sink.push {
		sink.dest.controlSecret, err = shared.RandomCryptoString()
		if err != nil {
			logger.Errorf("Failed to create migration sink secrect for control websocket")
			return nil, err
		}

		sink.dest.fsSecret, err = shared.RandomCryptoString()
		if err != nil {
			logger.Errorf("Failed to create migration sink secrect for filesystem websocket")
			return nil, err
		}
	} else {
		sink.src.controlSecret, ok = args.Secrets["control"]
		if !ok {
			logger.Errorf("Missing migration sink secrect for control websocket")
			return nil, fmt.Errorf("Missing control secret")
		}

		sink.src.fsSecret, ok = args.Secrets["fs"]
		if !ok {
			logger.Errorf("Missing migration sink secrect for filesystem websocket")
			return nil, fmt.Errorf("Missing fs secret")
		}
	}

	return &sink, nil
}

func (c *migrationSink) DoStorage(migrateOp *operation) error {
	var err error

	if c.push {
		<-c.allConnected
	}

	disconnector := c.src.disconnect
	if c.push {
		disconnector = c.dest.disconnect
	}

	if c.push {
		defer disconnector()
	} else {
		c.src.controlConn, err = c.connectWithSecret(c.src.controlSecret)
		if err != nil {
			logger.Errorf("Failed to connect migration sink control socket")
			return err
		}
		defer c.src.disconnect()

		c.src.fsConn, err = c.connectWithSecret(c.src.fsSecret)
		if err != nil {
			logger.Errorf("Failed to connect migration sink filesystem socket")
			c.src.sendControl(err)
			return err
		}
	}

	receiver := c.src.recv
	if c.push {
		receiver = c.dest.recv
	}

	sender := c.src.send
	if c.push {
		sender = c.dest.send
	}

	controller := c.src.sendControl
	if c.push {
		controller = c.dest.sendControl
	}

	header := migration.MigrationHeader{}
	if err := receiver(&header); err != nil {
		logger.Errorf("Failed to receive storage volume migration header")
		controller(err)
		return err
	}

	mySink := c.src.storage.StorageMigrationSink
	myType := c.src.storage.MigrationType()
	resp := migration.MigrationHeader{
		Fs: &myType,
	}

	// If the storage type the source has doesn't match what we have, then
	// we have to use rsync.
	if *header.Fs != *resp.Fs {
		mySink = rsyncStorageMigrationSink
		myType = migration.MigrationFSType_RSYNC
		resp.Fs = &myType
	}

	err = sender(&resp)
	if err != nil {
		logger.Errorf("Failed to send storage volume migration header")
		controller(err)
		return err
	}

	var fsConn *websocket.Conn
	if c.push {
		fsConn = c.dest.fsConn
	} else {
		fsConn = c.src.fsConn
	}

	err = mySink(fsConn, migrateOp, c.dest.storage)
	if err != nil {
		logger.Errorf("Failed to start storage volume migration sink")
		controller(err)
		return err
	}

	controller(nil)
	logger.Debugf("Migration sink finished receiving storage volume")
	return nil
}

func (s *migrationSourceWs) ConnectStorageTarget(target api.StorageVolumePostTarget) error {
	logger.Debugf("Storage migration source is connecting")
	return s.ConnectTarget(target.Certificate, target.Operation, target.Websockets)
}
